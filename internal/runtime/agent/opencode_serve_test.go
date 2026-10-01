package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gopermission "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecideOpencodePermission(t *testing.T) {
	cases := []struct {
		posture          gopermission.Mode
		permission, tool string
		want             string
	}{
		{gopermission.ModeYolo, "external_directory", "edit", "once"},
		{gopermission.ModeYolo, "bash", "", "once"},
		{gopermission.ModeYolo, "doom_loop", "", "once"},
		{gopermission.ModeDefault, "external_directory", "read", "once"},
		{gopermission.ModeDefault, "external_directory", "grep", "once"},
		{gopermission.ModeDefault, "external_directory", "write", "reject"},
		{gopermission.ModeDefault, "external_directory", "bash", "reject"},
		{gopermission.ModeDefault, "external_directory", "", "reject"},
		{gopermission.ModeDefault, "read", "read", "once"},
		{gopermission.ModeDefault, "edit", "edit", "reject"},
		{gopermission.ModeDefault, "bash", "bash", "reject"},
		{gopermission.ModeDefault, "webfetch", "webfetch", "reject"},
		{gopermission.ModeAcceptEdits, "edit", "write", "once"},
		{gopermission.ModeAcceptEdits, "external_directory", "read", "once"},
		{gopermission.ModeAcceptEdits, "external_directory", "edit", "reject"},
		{gopermission.ModeAcceptEdits, "bash", "bash", "reject"},
		{gopermission.ModePlan, "external_directory", "read", "reject"},
		{gopermission.ModePlan, "read", "read", "reject"},
	}
	for _, tc := range cases {
		got, reason := decideOpencodePermission(tc.posture, tc.permission, tc.tool)
		assert.Equal(t, tc.want, got, "%s %s (%s): %s", tc.posture, tc.permission, tc.tool, reason)
	}
}

func TestOpencodeServeEnv(t *testing.T) {
	oc := &provider.OpencodeAdapter{Agent: "worker", Model: "opencode/claude-sonnet-4-5"}
	content := func(env []string) map[string]any {
		t.Helper()
		var out map[string]any
		n := 0
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "OPENCODE_CONFIG_CONTENT="); ok {
				n++
				require.NoError(t, json.Unmarshal([]byte(v), &out))
			}
		}
		require.LessOrEqual(t, n, 1, "OPENCODE_CONFIG_CONTENT set twice: %q", env)
		return out
	}

	got := opencodeServeEnv([]string{"HOME=/home/agent"}, RuntimeKindServeHTTP, oc)
	assert.Equal(t, map[string]any{"model": "opencode/claude-sonnet-4-5", "default_agent": "worker"}, content(got))
	assert.Contains(t, got, "HOME=/home/agent")

	inherited := []string{`OPENCODE_CONFIG_CONTENT={"model":"other/model","theme":"dark"}`}
	assert.Equal(t, map[string]any{"model": "opencode/claude-sonnet-4-5", "default_agent": "worker", "theme": "dark"},
		content(opencodeServeEnv(inherited, RuntimeKindServeHTTP, oc)), "the profile wins; other inherited keys stay")

	plain := []string{"HOME=/home/agent"}
	assert.Equal(t, plain, opencodeServeEnv(plain, RuntimeKindSubprocess, oc), "opencode run takes --model/--agent")
	assert.Equal(t, plain, opencodeServeEnv(plain, RuntimeKindServeHTTP, &provider.ClaudeAdapter{}), "not opencode")
}

// replyServer records opencode permission replies.
type replyServer struct {
	mu      sync.Mutex
	replies []string // "<id> <directory> <body>"
}

func (s *replyServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /permission/{id}/reply", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.replies = append(s.replies, r.PathValue("id")+" "+r.URL.Query().Get("directory")+" "+string(b))
		s.mu.Unlock()
		_, _ = w.Write([]byte("true"))
	})
	return mux
}

func (s *replyServer) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.replies...)
}

// The responder learns the listen URL and each call's tool from the
// stdout lines, then answers permission.asked by posture, once per prompt,
// and logs each decision.
func TestOpencodePermissionResponder(t *testing.T) {
	rs := &replyServer{}
	srv := httptest.NewServer(rs.handler())
	defer srv.Close()
	var log bytes.Buffer
	r := newOpencodePermissionResponder(gopermission.ModeDefault, "/work/project", &log)

	asked := func(id, permission, callID string) string {
		return fmt.Sprintf(`{"type":"permission.asked","properties":{"id":%q,"sessionID":"ses_1","permission":%q,"patterns":["/elsewhere/*"],"metadata":{},"always":["/elsewhere/*"],"tool":{"messageID":"msg_1","callID":%q}}}`, id, permission, callID)
	}
	r.observe("opencode server listening on " + srv.URL)
	r.observe(`{"type":"message.part.updated","properties":{"sessionID":"ses_1","part":{"type":"tool","callID":"call_read","tool":"read"},"time":1}}`)
	r.observe(`{"directory":"/work/project","payload":{"type":"message.part.updated","properties":{"part":{"type":"tool","callID":"call_bash","tool":"bash"}}}}`)
	r.observe(asked("per_read", "external_directory", "call_read"))
	r.observe(asked("per_read", "external_directory", "call_read")) // seen twice, answered once
	r.observe(`{"directory":"/work/project","payload":` + asked("per_bash", "external_directory", "call_bash") + `}`)
	r.wait()

	assert.ElementsMatch(t, []string{
		`per_read /work/project {"reply":"once"}`,
		`per_bash /work/project {"message":"Torque declined this: external_directory (bash) is not granted under default (the profile's permission_mode).","reply":"reject"}`,
	}, rs.got())
	assert.Contains(t, log.String(), `opencode permission: permission=external_directory tool=read patterns="/elsewhere/*" posture=default reply=once (external_directory (read) is granted under default)`)
	assert.Contains(t, log.String(), `tool=bash patterns="/elsewhere/*" posture=default reply=reject`)
}

// Without a listen URL the reply cannot be sent; the log says so instead
// of the session hanging silently.
func TestOpencodePermissionResponder_NoListenURL(t *testing.T) {
	var log bytes.Buffer
	r := newOpencodePermissionResponder(gopermission.ModeYolo, "", &log)
	r.observe(`{"type":"permission.asked","properties":{"id":"per_1","permission":"bash","patterns":["ls"]}}`)
	r.wait()
	assert.Contains(t, log.String(), "reply=once not delivered: the serve listen URL is unknown")
}

func TestOpencodeListenURL(t *testing.T) {
	assert.Equal(t, "http://127.0.0.1:4096", opencodeListenURL("opencode server listening on http://127.0.0.1:4096"))
	assert.Equal(t, "http://127.0.0.1:4096", opencodeListenURL("listening on http://127.0.0.1:4096."))
	assert.Empty(t, opencodeListenURL("starting server"))
}
