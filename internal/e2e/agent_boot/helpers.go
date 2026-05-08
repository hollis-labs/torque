package agent_boot

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
)

// composedDeps bundles the substrate the agent_boot tests drive: a real
// agent.Manager bound to a real *sqlstore.Store, with the runtime layer
// replaced by a fakeRuntime via Dependencies.RuntimeFactory.
//
// Returned cleanup must run before the test ends — it bounds the inner
// Manager.Shutdown so leaked watch goroutines surface as a failure rather
// than a hang, and closes the SQLite handle.
type composedDeps struct {
	Deps    *agent.Dependencies
	Manager *agent.Manager
	Store   *sqlstore.Store
	Runtime *fakeRuntime

	// DB is the raw *sql.DB the Store wraps. Exposed so broker_test can
	// wire clockmsg.NewStore(db) against the same handle (envelope rows
	// sit alongside session rows under one set of migrations).
	DB *sql.DB
}

// composeDeps materializes the substrate. cfg picks the fakeRuntime's caps
// + failure injection; profileProvider is the agent profile's provider name
// (drives Boot's adapterFor / shouldUsePTY decisions; tests typically pick
// "claude" because its BootDirSpec is fully concrete and sets the PTY-on
// matrix default).
func composeDeps(t *testing.T, cfg fakeRuntimeConfig, profileProvider string) *composedDeps {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "agent_boot.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	rt := newFakeRuntime(cfg)

	prof := config.AgentProfile{Executor: "cli", Provider: profileProvider}
	deps := &agent.Dependencies{
		Store: store,
		// Register the profile under every name the agent_boot tests stamp
		// on agent.Options.AgentProfile — boot tests use "clockwork-backend",
		// the planstart e2e uses orchestrator.Profile ("orchestrator"), the
		// broker e2e uses "alice" / "bob". One profile, many lookup keys.
		Profiles: config.ProfileMap{
			"clockwork-backend": prof,
			"orchestrator":      prof,
			"alice":             prof,
			"bob":               prof,
			"default":           prof,
		},
		Loopback:       nil, // disable per-task MCP loopback in tests
		WorkspacesRoot: filepath.Join(dir, "workspaces"),
		RuntimeFactory: func(_ agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			return rt, nil
		},
	}
	deps.Sessions = agent.NewManager(deps)

	t.Cleanup(func() {
		// Stop every fakeSession before Shutdown so each watch goroutine
		// can drain. fakeSession.Wait blocks on a done chan that only Stop
		// closes — without this the bounded Shutdown below would either
		// fail (best case) or, on tests that booted more than one session,
		// leak goroutines from the un-stopped sessions across iterations
		// (count=10 / -race amplifies the leak into a flake).
		for _, sess := range rt.allSessions() {
			_ = sess.Stop(context.Background())
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := deps.Sessions.Shutdown(shutdownCtx); err != nil {
			t.Errorf("agent.Manager.Shutdown returned error: %v", err)
		}
		cancel()
		store.Close()
		_ = db.Close()
	})

	return &composedDeps{
		Deps:    deps,
		Manager: deps.Sessions,
		Store:   store,
		Runtime: rt,
		DB:      db,
	}
}

// plantCheckpoint inserts a session row + a session_checkpoints row whose
// resume_hint == providerSessionID. agent.Boot's findCheckpoint resolves
// the checkpoint by ID; the resume_hint bytes feed StartOptions.SessionIDPreset.
func plantCheckpoint(t *testing.T, store *sqlstore.Store, sessID, providerSessionID string) string {
	t.Helper()
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
		ID:           sessID,
		AgentProfile: "clockwork-backend",
		Provider:     "claude",
		RuntimeID:    "fake-runtime",
		RuntimeKind:  "fake",
		Workdir:      t.TempDir(),
		State:        "done",
		MetaJSON:     "{}",
	}))
	cpID := "SCP-" + ulid.Make().String()
	require.NoError(t, store.CreateSessionCheckpoint(&sqlstore.SessionCheckpointRecord{
		ID:         cpID,
		SessionID:  sessID,
		Payload:    `{"why":"agent_boot test plant"}`,
		ResumeHint: []byte(providerSessionID),
		Note:       "agent_boot test plant",
	}))
	return cpID
}
