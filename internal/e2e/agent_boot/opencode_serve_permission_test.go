package agent_boot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/harness/adapters/providertest"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// The overnight smoke's opencode serve session hung on its first
// permission.asked (an external_directory read outside the worktree)
// because nothing answered it, and ran opencode's default model as its
// built-in agent (CW-20261001-0148). This boots serve-http through the
// production wrapper path against a fake `opencode serve` and an httptest
// stand-in for its API. After the first prompt the server asks twice:
// external_directory for a `read`, then for a `bash` call. Under a default
// profile Torque must reply once to the read and reject the command, log
// both to session.log, and start serve with the profile's model and
// Torque's planted agent in OPENCODE_CONFIG_CONTENT. serve's raw output
// shares session.log with those decisions: since agentkit v0.19.1 opens the
// log O_APPEND (CW-20261001-0158), its later writes land after Torque's
// lines instead of over them, so the frames serve sends after both
// decisions must leave them intact (CW-20261001-0157).
func TestBoot_OpencodeServeHTTPAnswersPermissions(t *testing.T) {
	const ocSession = "ses_serve_perms"
	var (
		mu      sync.Mutex
		replies = map[string]string{}
	)
	events, quit := make(chan string, 16), make(chan struct{})
	part := func(callID, tool string) string {
		return fmt.Sprintf(`{"type":"message.part.updated","properties":{"sessionID":%q,"part":{"type":"tool","callID":%q,"tool":%q},"time":1}}`, ocSession, callID, tool)
	}
	asked := func(id, callID string) string {
		return fmt.Sprintf(`{"type":"permission.asked","properties":{"id":%q,"sessionID":%q,"permission":"external_directory","patterns":["/elsewhere/*"],"metadata":{},"always":["/elsewhere/*"],"tool":{"messageID":"msg_1","callID":%q}}}`, id, ocSession, callID)
	}
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
		events <- part("call_read", "read")
		events <- asked("per_read", "call_read")
	})
	mux.HandleFunc("POST /permission/{id}/reply", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reply string `json:"reply"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		replies[r.PathValue("id")] = body.Reply + " " + r.URL.Query().Get("directory")
		mu.Unlock()
		_, _ = w.Write([]byte("true"))
		switch r.PathValue("id") {
		case "per_read":
			events <- part("call_bash", "bash")
			events <- asked("per_bash", "call_bash")
		case "per_bash":
			events <- fmt.Sprintf(`{"type":"session.idle","properties":{"sessionID":%q}}`, ocSession)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { close(quit); srv.Close() })
	got := func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string]string, len(replies))
		for k, v := range replies {
			out[k] = v
		}
		return out
	}

	fake := providertest.New(t, runtimes.OpenCode, providertest.Script(
		providertest.Stdout("opencode server listening on "+srv.URL),
		providertest.Hang(),
	))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, string(runtimes.OpenCode))
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {
		Executor: "cli", Provider: "opencode", RuntimeKind: "http-sse",
		Model: "opencode/claude-sonnet-4-5", PermissionMode: string(config.PermissionModeDefault),
	}}
	workdir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const sessID = "SES-SERVE-PERMS"
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-TEST-SERVE-PERMS", AgentProfile: "worker", Workdir: workdir,
		Mode: agent.ModeLongLived, IDFn: func() string { return sessID },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	require.Eventually(t, func() bool { return len(got()) == 2 }, 10*time.Second, 20*time.Millisecond, "Torque never answered both prompts: %v", got())
	assert.Equal(t, map[string]string{"per_read": "once " + workdir, "per_bash": "reject " + workdir}, got())

	sessionLog := filepath.Join(cd.Deps.WorkspacesRoot, "unscoped", sessID, "logs", "session.log")
	require.Eventually(t, func() bool {
		b, _ := os.ReadFile(sessionLog)
		return strings.Count(string(b), "opencode permission:") == 2
	}, 5*time.Second, 20*time.Millisecond, "both decisions belong in session.log")
	logText, _ := os.ReadFile(sessionLog)
	assert.Contains(t, string(logText), "permission=external_directory tool=read")
	assert.Contains(t, string(logText), "posture=default reply=once")
	assert.Contains(t, string(logText), "tool=bash")
	assert.Contains(t, string(logText), "posture=default reply=reject")

	// agentkit keeps writing serve's raw output to the same session.log after
	// Torque's decisions: with agentkit's old os.Create log its writes
	// landed at its own offset, over the lines above.
	const late = 50
	for i := range late {
		events <- fmt.Sprintf(`{"type":"message.part.updated","properties":{"sessionID":%q,"part":{"type":"text","text":"late-frame-%d"},"time":2}}`, ocSession, i)
	}
	require.Eventually(t, func() bool {
		b, _ := os.ReadFile(sessionLog)
		return strings.Contains(string(b), fmt.Sprintf("late-frame-%d", late-1))
	}, 5*time.Second, 20*time.Millisecond, "agentkit writes serve's later frames to session.log")
	logText, _ = os.ReadFile(sessionLog)
	assert.Equal(t, 2, strings.Count(string(logText), "opencode permission:"), "Torque's decision lines survive agentkit's later writes")
	assert.Contains(t, string(logText), "opencode server listening on", "serve's raw output is in session.log")
	assert.Contains(t, string(logText), `"type":"permission.asked"`)
	for i := range late {
		assert.Contains(t, string(logText), fmt.Sprintf("late-frame-%d\"", i))
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(sessionLog), "serve-http.log"))
	assert.True(t, os.IsNotExist(err), "no separate serve-http.log")

	content, ok := fake.Call(0).Getenv("OPENCODE_CONFIG_CONTENT")
	require.True(t, ok, "serve must start with the profile's model and agent")
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(content), &cfg))
	assert.Equal(t, "opencode/claude-sonnet-4-5", cfg["model"])
	assert.Equal(t, "worker", cfg["default_agent"])
	assert.FileExists(t, filepath.Join(sess.BootDir, "agents", "worker.md"), "default_agent must name the planted agent")
}
