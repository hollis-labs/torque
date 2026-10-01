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
	start := time.Now()
	res, cd, taskID := runOpencodeServeTurn(t, ocSession, []string{
		fmt.Sprintf(`{"id":"evt_1","type":"session.status","properties":{"sessionID":%q,"status":{"type":"busy"}}}`, ocSession),
		fmt.Sprintf(`{"id":"evt_2","type":"session.error","properties":{"sessionID":%q,"error":{"name":"UnknownError","data":{"message":"Model not found: opencode/claude-sonnet-4-5. Did you mean: claude-sonnet-4-5?"}}}}`, ocSession),
		fmt.Sprintf(`{"id":"evt_3","type":"session.status","properties":{"sessionID":%q,"status":{"type":"idle"}}}`, ocSession),
		fmt.Sprintf(`{"id":"evt_4","type":"session.idle","properties":{"sessionID":%q}}`, ocSession),
		fmt.Sprintf(`{"id":"evt_5","type":"session.error","properties":{"sessionID":%q,"error":{"name":"UnknownError","data":{"message":"ProviderModelNotFoundError: Model not found: opencode/claude-sonnet-4-5. Did you mean: claude-sonnet-4-5?\n    at <anonymous> (/$bunfs/root/chunk-dn9bw1yz.js:439:94601)"}}}}`, ocSession),
	})
	assert.Less(t, time.Since(start), 10*time.Second)
	assert.Equal(t, "blocked", res.Status)
	assert.Equal(t, "Model not found: opencode/claude-sonnet-4-5. Did you mean: claude-sonnet-4-5?", res.Reason,
		"the reason is opencode's message, not the raw event or its stack trace")

	// The session stays failed, as a Codex run's does: the wrapper's own
	// end-of-run write after Stop must not report it done.
	sessions, err := cd.Store.ListSessions(sqlstore.SessionFilter{TaskID: taskID})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "failed", sessions[0].State)
	require.True(t, sessions[0].ExitCode.Valid)
	assert.EqualValues(t, -1, sessions[0].ExitCode.Int64)
}

// opencode serve also reports errors that do not end its turn, and agentkit
// makes each a failed turn: a skill it cannot parse (session.error naming
// no session), and a context overflow, after which opencode compacts the
// session and carries on. Neither may end a healthy run. Here the turn
// carries on after both and then fails for real; the run ends on that
// failure, with its message.
func TestRunLongLived_OpencodeServeErrorsThatContinueKeepTheRun(t *testing.T) {
	const ocSession = "ses_overflow"
	const realFailure = "Model not found: opencode/claude-sonnet-4-5."
	res, _, _ := runOpencodeServeTurn(t, ocSession, []string{
		`{"id":"evt_0","type":"session.error","properties":{"error":{"name":"UnknownError","data":{"message":"Failed to parse skill review: bad frontmatter"}}}}`,
		fmt.Sprintf(`{"id":"evt_1","type":"session.status","properties":{"sessionID":%q,"status":{"type":"busy"}}}`, ocSession),
		fmt.Sprintf(`{"id":"evt_2","type":"session.error","properties":{"sessionID":%q,"error":{"name":"ContextOverflowError","data":{"message":"prompt is too long: 210000 tokens > 200000 maximum"}}}}`, ocSession),
		fmt.Sprintf(`{"id":"evt_3","type":"message.part.delta","properties":{"sessionID":%q,"delta":"compacted; continuing"}}`, ocSession),
		fmt.Sprintf(`{"id":"evt_4","type":"session.error","properties":{"sessionID":%q,"error":{"name":"UnknownError","data":{"message":%q}}}}`, ocSession, realFailure),
	})
	assert.Equal(t, "blocked", res.Status)
	assert.Equal(t, realFailure, res.Reason,
		"the run ended on the real failure, not on the skill or overflow error before it")
}

// runOpencodeServeTurn runs a long-lived scheduler run on the production
// wrapper path against a fake opencode serve that answers the kickoff
// prompt with events, replayed in order on /event, and returns the run's
// result. The run must end before the test's deadline.
func runOpencodeServeTurn(t *testing.T, ocSession string, events []string) (*executor.ExecutionResult, *composedDeps, string) {
	t.Helper()
	sse, quit := make(chan string, len(events)+1), make(chan struct{})
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
			case ev := <-sse:
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
		for _, ev := range events {
			sse <- ev
		}
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
	res, err := agent.NewExecutor(cd.Deps).Run(ctx, &executor.ExecutionJob{
		TaskID: taskID, Kind: "agent", AgentProfile: "worker", WorkingDir: t.TempDir(), RunID: 1,
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NoError(t, ctx.Err(), "the run must end on the failed turn, not on the test's deadline")
	return res, cd, taskID
}
