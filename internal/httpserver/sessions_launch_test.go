package httpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestLaunchSessionBody_StrictMeta(t *testing.T) {
	t.Run("object", func(t *testing.T) {
		var body launchSessionBody
		require.NoError(t, json.Unmarshal([]byte(`{"workdir":"/tmp/x","meta":{"owner":"mcp"}}`), &body))
		assert.Equal(t, map[string]string{"owner": "mcp"}, body.Meta)
	})

	t.Run("legacy JSON string", func(t *testing.T) {
		var body launchSessionBody
		require.NoError(t, json.Unmarshal([]byte(`{"workdir":"/tmp/x","meta":"{\"owner\":\"mcp\"}"}`), &body))
		assert.Equal(t, map[string]string{"owner": "mcp"}, body.Meta)
	})

	for _, raw := range []string{
		`{"workdir":"/tmp/x","meta":{"owner":null}}`,
		`{"workdir":"/tmp/x","meta":{"owner":7}}`,
		`{"workdir":"/tmp/x","meta":"{\"owner\":false}"}`,
		`{"workdir":"/tmp/x","meta":[]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var body launchSessionBody
			require.Error(t, json.Unmarshal([]byte(raw), &body))
		})
	}
}

func TestLaunchSessionHTTP_ForwardsEnvMetaThroughBoot(t *testing.T) {
	srv, store, rt := launchSessionFixture(t)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	workdir := t.TempDir()

	body := `{
		"launch_profile":"worker.implementer",
		"agent_profile":"typo-ignored",
		"workdir":` + strconv.Quote(workdir) + `,
		"project_id":"PRJ-HTTP",
		"task_id":"CW-HTTP",
		"system_prompt":"hello",
		"env":["HTTP_ENV=ok","TORQUE_TASK_ID=caller-wins"],
		"meta":{
			"caller":"ok",
			"torque.custom":"keep",
			"torque.mode":"caller-mode",
			"torque.workspace_dir":"caller-ws",
			"torque.boot_dir":"caller-boot",
			"torque.parent_session_id":"caller-parent",
			"torque.run_id":"999"
		}
	}`
	resp, err := http.Post(ts.URL+"/api/v1/sessions/launch", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, rt.lastStart)

	env := envMap(rt.lastStart.Env)
	assert.Equal(t, "ok", env["HTTP_ENV"])
	assert.Equal(t, "caller-wins", env["TORQUE_TASK_ID"], "caller env should preserve existing composeEnv precedence")

	got, err := store.GetSession("SES-HTTP-1")
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
	assert.NotContains(t, meta, "torque.parent_session_id")
	assert.NotContains(t, meta, "torque.run_id")

	sess, err := srv.sessions.Get("SES-HTTP-1")
	require.NoError(t, err)
	assert.Equal(t, agent.ModeLongLived, sess.Mode)
	assert.Equal(t, meta["torque.workspace_dir"], sess.WorkspaceDir)
	assert.Equal(t, meta["torque.boot_dir"], sess.BootDir)
	assert.Empty(t, sess.ParentSessionID)
	assert.Equal(t, "keep", sess.Meta["torque.custom"])
}

func TestLaunchSessionHTTP_InvalidMetaRejectedBeforeBoot(t *testing.T) {
	srv, _, rt := launchSessionFixture(t)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/api/v1/sessions/launch", "application/json",
		bytes.NewBufferString(`{"agent_profile":"default","workdir":`+strconv.Quote(t.TempDir())+`,"meta":{"owner":null}}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Nil(t, rt.lastStart)
}

func launchSessionFixture(t *testing.T) (*Server, *sqlstore.Store, *httpRecordingRuntime) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	rt := &httpRecordingRuntime{}
	prof := config.AgentProfile{Executor: "cli", Provider: "claude-code"}
	deps := &agent.Dependencies{
		Store:          store,
		Profiles:       config.ProfileMap{"default": prof},
		WorkspacesRoot: t.TempDir(),
		RuntimeFactory: func(cfg agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			rt.kind = cfg.Kind
			return rt, nil
		},
	}
	deps.Sessions = agent.NewManager(deps).WithIDFunc(func() string { return "SES-HTTP-1" })
	srv := New(nil, nil).WithSessions(deps.Sessions)
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
	return srv, store, rt
}

type httpRecordingRuntime struct {
	kind        string
	lastStart   *agentsessions.StartOptions
	lastSession *httpRecordingSession
}

func (r *httpRecordingRuntime) ID() string   { return "http-recording-runtime" }
func (r *httpRecordingRuntime) Kind() string { return r.kind }
func (r *httpRecordingRuntime) Caps() agentsessions.Capabilities {
	return agentsessions.Capabilities{}
}
func (r *httpRecordingRuntime) Prepare(context.Context) error { return nil }
func (r *httpRecordingRuntime) Start(_ context.Context, opts agentsessions.StartOptions) (agentsessions.Session, error) {
	snap := opts
	r.lastStart = &snap
	sess := &httpRecordingSession{done: make(chan struct{})}
	r.lastSession = sess
	return sess, nil
}

type httpRecordingSession struct {
	done chan struct{}
}

func (s *httpRecordingSession) Wait() (int, error) {
	<-s.done
	return 0, nil
}
func (s *httpRecordingSession) Stop(context.Context) error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}
func (s *httpRecordingSession) SendInput(context.Context, []byte) error { return nil }
func (s *httpRecordingSession) Resize(context.Context, uint16, uint16) error {
	return nil
}
func (s *httpRecordingSession) Health() agentsessions.HealthStatus {
	return agentsessions.HealthStatus{Alive: true, State: agentsessions.LiveStateIdle}
}
func (s *httpRecordingSession) CheckpointHints() (agentsessions.CheckpointHint, bool) {
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
