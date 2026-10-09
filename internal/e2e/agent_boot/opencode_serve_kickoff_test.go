package agent_boot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/harness/adapters/providertest"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// opencode serve runs in the project dir, where `@./boot.md` names nothing,
// so its first turn must carry boot.md's content (CW-20261001-0104), and each
// later turn its own text. This boots serve-http through Torque's production
// (wrapper) path: a fake `opencode serve` prints the listen URL of an
// httptest server standing in for opencode's HTTP API, which records each
// prompt_async text and ends the turn with session.idle (CW-20261001-0121).
func TestBoot_OpencodeServeHTTPKickoffIsBootContent(t *testing.T) {
	const sessionID = "ses_serve_kickoff"
	var (
		mu      sync.Mutex
		prompts []string
	)
	events, quit := make(chan string, 8), make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /global/health", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /session", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":%q}`, sessionID)
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
	mux.HandleFunc("POST /session/{id}/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Parts) != 1 || r.PathValue("id") != sessionID {
			http.Error(w, "bad prompt", http.StatusBadRequest)
			return
		}
		mu.Lock()
		prompts = append(prompts, body.Parts[0].Text)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		events <- fmt.Sprintf(`{"type":"session.idle","properties":{"sessionID":%q}}`, sessionID)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { close(quit); srv.Close() })
	got := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), prompts...) }

	fake := providertest.New(t, runtimes.OpenCode, providertest.Script(
		providertest.Stdout("opencode server listening on "+srv.URL),
		providertest.Hang(),
	))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, string(runtimes.OpenCode))
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode", RuntimeKind: "http-sse"}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-TEST-SERVE-KICKOFF", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	require.Equal(t, []string{"serve", "--port", "0", "--hostname", "127.0.0.1"}, fake.Call(0).Args)

	require.NotEmpty(t, sess.BootDir)
	bootMD, err := os.ReadFile(filepath.Join(sess.BootDir, "boot.md"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(got()) >= 1 }, 10*time.Second, 20*time.Millisecond, "the first turn never reached the server")
	require.Equal(t, string(bootMD), got()[0], "the first turn must be boot.md's content")
	require.NotContains(t, got()[0], "@./boot.md")

	const turnTwo = "turn two: report status"
	require.Eventually(t, func() bool { return cd.Manager.SendTurn(ctx, sess, turnTwo) == nil }, 10*time.Second, 50*time.Millisecond, "turn 2 was refused")
	require.Eventually(t, func() bool { return len(got()) >= 2 }, 10*time.Second, 20*time.Millisecond, "turn 2 never reached the server")
	require.Equal(t, []string{string(bootMD), turnTwo}, got())
}
