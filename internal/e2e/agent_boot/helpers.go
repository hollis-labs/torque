package agent_boot

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/agent"
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
// (drives Boot's adapterFor / runtime-kind decisions; tests typically pick
// "claude-code" because its BootDirSpec is fully concrete and it resolves to
// the streaming-stdio runtime kind — the bare "claude" provider was retired
// 2026-05-16 and adapterFor now rejects it).
func composeDeps(t *testing.T, cfg fakeRuntimeConfig, profileProvider string) *composedDeps {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlitekit.OpenWriter(context.Background(), filepath.Join(dir, "agent_boot.db"), sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions()})
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	rt := newFakeRuntime(cfg)
	rtFactoryErr := cfg.RuntimeFactoryErr

	prof := config.AgentProfile{Executor: "cli", Provider: profileProvider}
	deps := &agent.Dependencies{
		Store: store,
		// Register the profile under every name the agent_boot tests stamp
		// on agent.Options.AgentProfile — boot tests use "torque-backend",
		// the planstart e2e uses orchestrator.Profile ("orchestrator"), the
		// broker e2e uses "alice" / "bob". One profile, many lookup keys.
		Profiles: config.ProfileMap{
			"torque-backend": prof,
			"orchestrator":   prof,
			"alice":          prof,
			"bob":            prof,
			"default":        prof,
		},
		Loopback:       nil, // disable per-task MCP loopback in tests
		WorkspacesRoot: filepath.Join(dir, "workspaces"),
		RuntimeFactory: func(cfg agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			// Injected runtime-construction failure. agent.Boot calls the
			// factory AFTER providerplant.Plant materializes the boot dir,
			// so returning an error here exercises Boot's intermediate
			// pre-Start failure path (the boot-dir leak regression test
			// asserts the planted dir is reaped on this path).
			if rtFactoryErr != nil {
				return nil, rtFactoryErr
			}
			// Capture the adapter so fakeRuntime.Start can simulate the
			// lib's preparePlant under AutoPlantBootDir (walks
			// adapter.BootDirSpec().PlantedFiles to materialize CLAUDE.md
			// / boot.md / .mcp.json / .claude/settings.json the same way
			// agentsessions.preparePlant does in production).
			rt.adapter = cfg.Adapter
			// Capture the resolved RuntimeKind from cfg.Kind so the fake
			// runtime's Kind() returns the same string a real lib
			// runtime would. This is what flows onto Session.RuntimeKind
			// and what Manager.SendTurn routes by — without it, every
			// fakeRuntime session would report Kind="fake" regardless of
			// the per-provider matrix selection.
			if cfg.Kind != "" {
				rt.kind = cfg.Kind
			}
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
		AgentProfile: "torque-backend",
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
