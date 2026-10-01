package agent_boot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// The overnight smoke's opencode serve worker (SES-01M3V2SD9FV5MW036H68N643MT)
// asked for a model opencode does not know. opencode answered the prompt
// with session.error twice, but the run stayed running with its task in
// doing until the inactivity threshold (CW-20261001-0169). This replays
// that session's event sequence, captured from its serve-http.log, through
// a long-lived scheduler run on the production wrapper path: the run must
// end at once, blocked, with opencode's message as its reason.
func TestRunLongLived_OpencodeServePromptErrorFailsTheRun(t *testing.T) {
	const ocSession = "ses_f09d32ee9ffehCdK5rrllGpq1y"
	events, quit := make(chan string, 16), make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /global/health", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /session", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":%q}`, ocSession)
	})
	mux.HandleFunc("GET /event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.(http.Flusher).Flush()
		for {
			select {
			case ev := <-events:
				_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			case <-quit:
				return
			}
		}
	})
	mux.HandleFunc("POST /session/{id}/prompt_async", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		events <- fmt.Sprintf(`{"id":"evt_1","type":"session.status","properties":{"sessionID":%q,"status":{"type":"busy"}}}`, ocSession)
		events <- fmt.Sprintf(`{"id":"evt_2","type":"session.error","properties":{"sessionID":%q,"error":{"name":"UnknownError","data":{"message":"Model not found: opencode/claude-sonnet-4-5. Did you mean: claude-sonnet-4-5?"}}}}`, ocSession)
		events <- fmt.Sprintf(`{"id":"evt_3","type":"session.status","properties":{"sessionID":%q,"status":{"type":"idle"}}}`, ocSession)
		events <- fmt.Sprintf(`{"id":"evt_4","type":"session.idle","properties":{"sessionID":%q}}`, ocSession)
		events <- fmt.Sprintf(`{"id":"evt_5","type":"session.error","properties":{"sessionID":%q,"error":{"name":"UnknownError","data":{"message":"ProviderModelNotFoundError: Model not found: opencode/claude-sonnet-4-5. Did you mean: claude-sonnet-4-5?\n    at <anonymous> (/$bunfs/root/chunk-dn9bw1yz.js:439:94601)"}}}}`, ocSession)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { close(quit); srv.Close() })

	fake := providertest.New(t, runtimes.OpenCode, providertest.Script(
		providertest.Stdout("opencode server listening on "+srv.URL),
		providertest.Hang(),
	))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, string(runtimes.OpenCode))
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {
		Executor: "cli", Provider: "opencode", RuntimeKind: "http-sse",
		Model: "opencode/claude-sonnet-4-5", PermissionMode: string(config.PermissionModeAcceptEdits),
	}}
	const taskID = "CW-TEST-SERVE-ERROR"
	require.NoError(t, cd.Store.CreateTask(&sqlstore.TaskRecord{
		ID: taskID, Title: "serve prompt error", Status: "doing", Executor: "cli", Kind: "agent", AgentProfile: "worker",
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	res, err := agent.NewExecutor(cd.Deps).Run(ctx, &executor.ExecutionJob{
		TaskID: taskID, Kind: "agent", AgentProfile: "worker", WorkingDir: t.TempDir(), RunID: 1,
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NoError(t, ctx.Err(), "the run must end on the failed turn, not on the test's deadline")
	assert.Less(t, time.Since(start), 10*time.Second)
	assert.Equal(t, "blocked", res.Status)
	assert.Equal(t, "Model not found: opencode/claude-sonnet-4-5. Did you mean: claude-sonnet-4-5?", res.Reason,
		"the reason is opencode's message, not the raw event or its stack trace")
}
