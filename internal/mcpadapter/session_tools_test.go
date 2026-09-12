package mcpadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestHandleSessionCreate_UnknownAgentProfile(t *testing.T) {
	a := &Adapter{
		sessions: agent.NewManager(&agent.Dependencies{
			Profiles: config.ProfileMap{
				"default": {},
				"fast":    {},
			},
		}),
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"agent_profile": "typo",
		"workdir":       t.TempDir(),
	}

	res, err := a.handleSessionCreate(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, `"code": "arg_invalid"`)
	assert.Contains(t, text.Text, "unknown agent_profile 'typo' in profiles.yaml agent_profiles registry — known: [default fast]")
}

// TestHandleSessionCreate_NoSelectorSurfacesArgInvalid pins the MCP
// adapter's explicit pre-Boot check that at least one of launch_profile
// or agent_profile is supplied. agent.Options.Validate enforces the same
// invariant downstream, but surfacing it here as ErrCodeArgInvalid (with
// the field hint) lets clients self-correct rather than seeing the
// failure wrapped as a generic domain error.
func TestHandleSessionCreate_NoSelectorSurfacesArgInvalid(t *testing.T) {
	a := &Adapter{
		sessions: agent.NewManager(&agent.Dependencies{
			Profiles: config.ProfileMap{"default": {}},
		}),
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"workdir": t.TempDir(),
	}

	res, err := a.handleSessionCreate(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "missing selector must surface as IsError")
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, `"code": "arg_invalid"`)
	assert.Contains(t, text.Text, "launch_profile")
	assert.Contains(t, text.Text, "at least one of launch_profile or agent_profile is required")
}

func TestHandleSessionCreate_ForwardsEnvMetaThroughBoot(t *testing.T) {
	a, store, rt := sessionCreateFixture(t)
	workdir := t.TempDir()

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"launch_profile": "worker.implementer",
		"agent_profile":  "typo-ignored-when-launch-profile-present",
		"workdir":        workdir,
		"project_id":     "PRJ-1",
		"task_id":        "CW-1",
		"system_prompt":  "hello",
		"env": map[string]any{
			"VISIBLE": "yes",
		},
		"meta": `{"caller":"ok","torque.custom":"keep","torque.mode":"caller-mode","torque.workspace_dir":"caller-ws","torque.boot_dir":"caller-boot","torque.parent_session_id":"caller-parent","torque.run_id":"999"}`,
	}

	res, err := a.handleSessionCreate(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, "response: %#v", res.Content)
	require.NotNil(t, rt.lastStart, "Boot must reach the fake runtime")

	env := envMap(rt.lastStart.Env)
	assert.Equal(t, "yes", env["VISIBLE"])
	assert.Equal(t, "CW-1", env["TORQUE_TASK_ID"])

	got, err := store.GetSession("SES-MCP-1")
	require.NoError(t, err)
	assert.Equal(t, "worker.implementer", got.LaunchProfile)
	assert.Equal(t, "default", got.AgentProfile, "launch_profile must win over legacy agent_profile")

	var meta map[string]string
	require.NoError(t, json.Unmarshal([]byte(got.MetaJSON), &meta))
	assert.Equal(t, "ok", meta["caller"])
	assert.Equal(t, "keep", meta["torque.custom"])
	assert.Equal(t, "long_lived", meta["torque.mode"])
	assert.NotEmpty(t, meta["torque.workspace_dir"])
	assert.NotEqual(t, "caller-ws", meta["torque.workspace_dir"])
	assert.NotEmpty(t, meta["torque.boot_dir"])
	assert.NotEqual(t, "caller-boot", meta["torque.boot_dir"])
	assert.NotContains(t, meta, "torque.parent_session_id", "caller-owned protected parent_session_id must be stripped when no actual value is stamped")
	assert.NotContains(t, meta, "torque.run_id", "caller-owned run_id authority must be stripped")

	sess, err := a.sessions.Get("SES-MCP-1")
	require.NoError(t, err)
	assert.Equal(t, agent.ModeLongLived, sess.Mode)
	assert.Equal(t, meta["torque.workspace_dir"], sess.WorkspaceDir)
	assert.Equal(t, meta["torque.boot_dir"], sess.BootDir)
	assert.Empty(t, sess.ParentSessionID)
	assert.NotContains(t, sess.Meta, "torque.run_id")
	assert.Equal(t, "keep", sess.Meta["torque.custom"])
}

func TestSessionCreateRegisteredToolSchemaAndNativeMapCall(t *testing.T) {
	a, store, rt := sessionCreateFixture(t)
	tools := a.server.ListTools()
	for _, name := range []string{"torque_session_create", "torque_session_launch"} {
		tool := tools[name]
		require.NotNil(t, tool, "%s must be registered", name)
		for _, field := range []string{"env", "meta"} {
			prop, ok := tool.Tool.InputSchema.Properties[field].(map[string]any)
			require.True(t, ok, "%s.%s schema missing", name, field)
			require.Contains(t, prop["description"], "native object")
			anyOf, ok := prop["anyOf"].([]any)
			require.True(t, ok, "%s.%s must advertise object|string union", name, field)
			require.Len(t, anyOf, 2)
			obj := anyOf[0].(map[string]any)
			assert.Equal(t, "object", obj["type"])
			assert.Equal(t, map[string]any{"type": "string"}, obj["additionalProperties"])
			assert.Equal(t, "string", anyOf[1].(map[string]any)["type"])
		}
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"agent_profile": "default",
		"workdir":       t.TempDir(),
		"env":           map[string]any{"NATIVE_ENV": "ok"},
		"meta":          map[string]any{"native_meta": "ok"},
	}
	res, err := tools["torque_session_launch"].Handler(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, "response: %#v", res.Content)
	require.NotNil(t, rt.lastStart)
	assert.Equal(t, "ok", envMap(rt.lastStart.Env)["NATIVE_ENV"])

	got, err := store.GetSession("SES-MCP-1")
	require.NoError(t, err)
	var meta map[string]string
	require.NoError(t, json.Unmarshal([]byte(got.MetaJSON), &meta))
	assert.Equal(t, "ok", meta["native_meta"])
}

func TestHandleSessionCreate_RejectsInvalidEnvMeta(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{name: "env scalar", field: "env", value: 12},
		{name: "env non-string member", field: "env", value: map[string]any{"A": true}},
		{name: "env malformed JSON string", field: "env", value: `{"A":`},
		{name: "meta scalar", field: "meta", value: "not-json"},
		{name: "meta null member", field: "meta", value: map[string]any{"owner": nil}},
		{name: "meta numeric member in legacy JSON", field: "meta", value: `{"owner":7}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _ := sessionCreateFixture(t)
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{
				"agent_profile": "default",
				"workdir":       t.TempDir(),
				tc.field:        tc.value,
			}

			res, err := a.handleSessionCreate(context.Background(), req)
			require.NoError(t, err)
			require.True(t, res.IsError)
			text := res.Content[0].(mcp.TextContent).Text
			assert.Contains(t, text, `"code": "arg_invalid"`)
			assert.Contains(t, text, tc.field)
		})
	}
}

func sessionCreateFixture(t *testing.T) (*Adapter, *sqlstore.Store, *recordingRuntime) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	rt := &recordingRuntime{}
	prof := config.AgentProfile{Executor: "cli", Provider: "claude-code"}
	deps := &agent.Dependencies{
		Store: store,
		Profiles: config.ProfileMap{
			"default": prof,
		},
		WorkspacesRoot: t.TempDir(),
		RuntimeFactory: func(cfg agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			rt.kind = cfg.Kind
			return rt, nil
		},
	}
	deps.Sessions = agent.NewManager(deps).WithIDFunc(func() string { return "SES-MCP-1" })
	a := &Adapter{
		sessions: deps.Sessions,
		server:   mcpserver.NewMCPServer("Torque Test", "0.0.0"),
	}
	a.registerSessionTools()
	t.Cleanup(func() {
		if rt.lastSession != nil {
			_ = rt.lastSession.Stop(context.Background())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, deps.Sessions.Shutdown(ctx))
		store.Close()
		require.NoError(t, db.Close())
	})
	return a, store, rt
}

type recordingRuntime struct {
	kind        string
	lastStart   *agentsessions.StartOptions
	lastSession *recordingSession
}

func (r *recordingRuntime) ID() string   { return "recording-runtime" }
func (r *recordingRuntime) Kind() string { return r.kind }
func (r *recordingRuntime) Caps() agentsessions.Capabilities {
	return agentsessions.Capabilities{}
}
func (r *recordingRuntime) Prepare(context.Context) error { return nil }
func (r *recordingRuntime) Start(_ context.Context, opts agentsessions.StartOptions) (agentsessions.Session, error) {
	snap := opts
	r.lastStart = &snap
	sess := &recordingSession{done: make(chan struct{})}
	r.lastSession = sess
	return sess, nil
}

type recordingSession struct {
	done chan struct{}
}

func (s *recordingSession) Wait() (int, error) {
	<-s.done
	return 0, nil
}
func (s *recordingSession) Stop(context.Context) error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}
func (s *recordingSession) SendInput(context.Context, []byte) error { return nil }
func (s *recordingSession) Resize(context.Context, uint16, uint16) error {
	return nil
}
func (s *recordingSession) Health() agentsessions.HealthStatus {
	return agentsessions.HealthStatus{Alive: true, State: agentsessions.LiveStateIdle}
}
func (s *recordingSession) CheckpointHints() (agentsessions.CheckpointHint, bool) {
	return nil, false
}

func envMap(values []string) map[string]string {
	out := make(map[string]string, len(values))
	for _, kv := range values {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				out[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return out
}
