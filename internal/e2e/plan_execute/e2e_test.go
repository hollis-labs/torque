//go:build agentboot_e2e_pending
// +build agentboot_e2e_pending

// CW-20260508-0001 migration note: this e2e file targets the deleted
// internal/runtime/sessionmgr package. Gated behind a build tag while the
// migration to fakeRuntime + agent.Manager + AutoFireFirstTurn assertions
// lands in P7 (per implementer prompt §"Existing tests to migrate"). The
// substrate-side coverage continues via internal/runtime/agent + bootstrap
// tests; this file's S2.5 substrate-composition shape needs a dedicated
// pass against the new agent.Manager.

// Package plan_execute_e2e is the unattended part of the S2 exit gate
// (CW-20260503-0021, S2.5). It validates the plan_start → orchestrator
// session boot path against a real sessionmgr.Manager + a fakeRuntime
// — substrate composition only, NO real LLM calls. The paid-LLM
// observations live in docs/superpowers/runbooks/plan-execute-smoke.md
// and are user-driven.
//
// This complements internal/e2e/sessionmgr_broker (S1.6 substrate
// smoke) by exercising the agent-layer trigger:
//   - planstart.Start validates kind=plan + status, boots the
//     orchestrator session through the real sessionmgr, stamps
//     metadata.plan.orchestrator_session_id
//   - The booted session appears in clockwork_session_list with
//     agent_profile=orchestrator, role=orchestrator in SessionMeta,
//     and plan_id=<PLAN>
//   - Idempotency: calling plan_start while the orchestrator is live
//     returns the existing session id without spawning a duplicate
package plan_execute_e2e

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/clockwork-manifold/internal/orchestrator"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/planstart"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/sessionmgr"
)

// ----- fake runtime (mirrors sessionmgr_broker e2e) ----------------------

type fakeSession struct {
	pid     int
	done    chan struct{}
	once    sync.Once
	dead    atomic.Bool
	runtime *fakeRuntime // back-ref so SendInput records first-turn dispatch on the runtime
}

func newFakeSession(pid int, rt *fakeRuntime) *fakeSession {
	return &fakeSession{pid: pid, done: make(chan struct{}), runtime: rt}
}

func (f *fakeSession) Wait() (int, error) {
	<-f.done
	return 0, nil
}
func (f *fakeSession) Stop(_ context.Context) error {
	f.dead.Store(true)
	f.once.Do(func() { close(f.done) })
	return nil
}
func (f *fakeSession) SendInput(_ context.Context, data []byte) error {
	if f.dead.Load() {
		return agentsessions.ErrNoInputChannel
	}
	if f.runtime != nil {
		f.runtime.firstTurnFired.Store(true)
		f.runtime.firstTurnPayloadLen.Store(int32(len(data)))
	}
	return nil
}
func (f *fakeSession) Resize(_ context.Context, _, _ uint16) error { return nil }
func (f *fakeSession) Health() agentsessions.HealthStatus {
	return agentsessions.HealthStatus{Alive: !f.dead.Load(), PID: f.pid}
}
func (f *fakeSession) CheckpointHints() (agentsessions.CheckpointHint, bool) {
	return nil, false
}

// fakeRuntime tracks whether the first-turn dispatch (Manager.SendInput
// → Session.SendInput) actually drove the runtime. Tracking on the
// session's SendInput rather than Runtime.Start matters because
// agentsessions.Manager.Start calls Runtime.Start synchronously during
// Launch — so a Runtime.Start tracker would be true even before the
// first-turn fix and wouldn't validate the fix. SendInput is the
// post-Launch call that actually spawns/drives the agent process for
// cli-runtime, so asserting it fired is the direct test.
type fakeRuntime struct {
	id                  string
	pidNext             atomic.Int32
	firstTurnFired      atomic.Bool
	firstTurnPayloadLen atomic.Int32
}

func newFakeRuntime(id string) *fakeRuntime {
	r := &fakeRuntime{id: id}
	r.pidNext.Store(3000)
	return r
}

func (r *fakeRuntime) ID() string                       { return r.id }
func (r *fakeRuntime) Kind() string                     { return "fake" }
func (r *fakeRuntime) Caps() agentsessions.Capabilities { return agentsessions.Capabilities{} }
func (r *fakeRuntime) Prepare(_ context.Context) error  { return nil }
func (r *fakeRuntime) Start(_ context.Context, _ agentsessions.StartOptions) (agentsessions.Session, error) {
	pid := int(r.pidNext.Add(1))
	return newFakeSession(pid, r), nil
}

type fakeRegistry struct{ rt *fakeRuntime }

func (r *fakeRegistry) RuntimeFor(_ string, _ []byte) (agentsessions.Runtime, error) {
	return r.rt, nil
}

type stubEmitter struct{}

func (stubEmitter) EmitSessionEvent(_ string, _ map[string]interface{}) {}

// ----- harness -----------------------------------------------------------

func setup(t *testing.T) (*sessionmgr.Manager, *sqlstore.Store, *fakeRuntime, func()) {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "smoke.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	rt := newFakeRuntime("rt-fake")
	mgr := sessionmgr.New(store, &fakeRegistry{rt: rt}, stubEmitter{})

	cleanup := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr.Shutdown(shutdownCtx)
		cancel()
		store.Close()
		db.Close()
	}
	return mgr, store, rt, cleanup
}

// createPlanTask inserts a minimal kind=plan record matching what the
// PlanService would create. We bypass the service to keep the test
// closed-loop and avoid pulling the validation rules through a path
// where they're already tested.
func createPlanTask(t *testing.T, store *sqlstore.Store, id, workdir string) *sqlstore.TaskRecord {
	t.Helper()
	plan := &sqlstore.TaskRecord{
		ID:           id,
		Title:        "Smoke plan: " + id,
		Description:  "Composition test for plan-start → orchestrator boot",
		Status:       "todo",
		Kind:         "plan",
		Executor:     "cli",
		AgentProfile: "clockwork-backend",
		WorkingDir:   workdir,
		ProjectID:    sql.NullString{String: "PRJ-SMOKE", Valid: true},
		Metadata: sql.NullString{
			String: `{"plan":{"version":1,"phases":[{"id":"ph-1","name":"write","order":1}]}}`,
			Valid:  true,
		},
	}
	require.NoError(t, store.CreateTask(plan))
	return plan
}

// ----- tests -------------------------------------------------------------

// AC subset (unattended): trigger-composition exit gate. Validates
// items 1, 4, and the substrate-side prerequisites for items 2, 3,
// 5–10. The remaining real-LLM observations are user-driven per the
// runbook.
func TestPlanExecute_TriggerBootsOrchestratorSession(t *testing.T) {
	mgr, store, rt, cleanup := setup(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	plan := createPlanTask(t, store, "CW-PLAN-SMOKE-001", t.TempDir())

	res, err := planstart.Start(ctx, store, mgr, plan.ID, planstart.Options{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.NotEmpty(t, res.SessionID, "orchestrator session id should be returned")
	assert.Equal(t, plan.ID, res.PlanID)

	// First-turn dispatch: planstart.Start must drive SendInput post-
	// Launch so the cli-runtime subprocess actually spawns. Without
	// this fire, sessionmgr.Launch only registers the row and the
	// agent process never starts (CW-20260507-0011).
	assert.True(t, rt.firstTurnFired.Load(),
		"planstart.Start must drive Manager.SendInput post-Launch to spawn the orchestrator process")
	assert.Greater(t, rt.firstTurnPayloadLen.Load(), int32(0),
		"first-turn payload must be non-empty")

	// Item 1: orchestrator session appears in clockwork_session_list.
	sessions, err := mgr.List(sessionmgr.StatusRunning, "", "", 0)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	sess := sessions[0]
	assert.Equal(t, res.SessionID, sess.ID)
	assert.Equal(t, orchestrator.Profile, sess.AgentProfile)
	assert.Equal(t, plan.ID, sess.TaskID, "session.task_id binds to the plan")

	// Session metadata carries plan_id + role tag (S2.2 contract).
	assert.Equal(t, plan.ID, sess.Meta[orchestrator.SessionMetaPlanID])
	assert.Equal(t, orchestrator.SessionMetaRoleValue, sess.Meta[orchestrator.SessionMetaRole])

	// Item 4 substrate prerequisite: plan transitioned to doing.
	post, err := store.GetTask(plan.ID)
	require.NoError(t, err)
	assert.Equal(t, "doing", post.Status, "plan must be in doing after start")

	// metadata.plan.orchestrator_session_id stamped (substrate-side
	// of items 5–10 — the orchestrator agent walks this metadata to
	// drive the plan).
	require.True(t, post.Metadata.Valid)
	assert.Contains(t, post.Metadata.String, res.SessionID)
	assert.Contains(t, post.Metadata.String, "orchestrator_session_id")
	assert.Contains(t, post.Metadata.String, "orchestrator_started_at")

	// Pre-existing plan.phases survives the metadata write — the
	// orchestrator needs it to walk phases.
	assert.Contains(t, post.Metadata.String, `"phases"`)

	// Stop the booted session before cleanup so Shutdown drains.
	require.NoError(t, mgr.Stop(ctx, res.SessionID))
	require.Eventually(t, func() bool {
		s, _ := mgr.Get(res.SessionID)
		return s != nil && s.Status.Terminal()
	}, 5*time.Second, 10*time.Millisecond, "orchestrator session must reach terminal state on Stop")
}

// AC5 idempotency: calling plan_start while an orchestrator session
// is live returns ErrAlreadyOrchestrating with the existing session id.
// Validates that the runbook's "Rerun" path works (item 2 of the
// "Rerun" section).
func TestPlanExecute_IdempotentWhileLive(t *testing.T) {
	mgr, store, _, cleanup := setup(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	plan := createPlanTask(t, store, "CW-PLAN-SMOKE-002", t.TempDir())

	first, err := planstart.Start(ctx, store, mgr, plan.ID, planstart.Options{})
	require.NoError(t, err)

	// Manually flip plan back to todo so we can re-attempt — in
	// practice the live-session check fires before status check, but
	// we want to exercise that path explicitly.
	require.NoError(t, store.TransitionTask(plan.ID, "todo"))

	second, err := planstart.Start(ctx, store, mgr, plan.ID, planstart.Options{})
	require.ErrorIs(t, err, planstart.ErrAlreadyOrchestrating)
	require.NotNil(t, second)
	assert.Equal(t, first.SessionID, second.SessionID, "second start surfaces the live session id")

	// Only ONE running orchestrator session — we did not spawn a duplicate.
	running, err := mgr.List(sessionmgr.StatusRunning, "", "", 0)
	require.NoError(t, err)
	require.Len(t, running, 1)

	require.NoError(t, mgr.Stop(ctx, first.SessionID))
	require.Eventually(t, func() bool {
		s, _ := mgr.Get(first.SessionID)
		return s != nil && s.Status.Terminal()
	}, 5*time.Second, 10*time.Millisecond)
}

// Wrong-kind / wrong-status / missing-plan paths surface the right
// sentinels — backstop for the runbook's failure-mode notes.
func TestPlanExecute_RejectsInvalidTargets(t *testing.T) {
	mgr, store, _, cleanup := setup(t)
	defer cleanup()

	ctx := context.Background()

	// Wrong kind.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-NOT-PLAN", Title: "x", Status: "todo", Kind: "agent",
		Executor: "cli", AgentProfile: "clockwork-backend",
	}))
	_, err := planstart.Start(ctx, store, mgr, "CW-NOT-PLAN", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrPlanNotFound)

	// Missing.
	_, err = planstart.Start(ctx, store, mgr, "CW-NOPE", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrPlanNotFound)

	// Wrong status.
	plan := createPlanTask(t, store, "CW-PLAN-DONE", t.TempDir())
	require.NoError(t, store.TransitionTask(plan.ID, "doing"))
	require.NoError(t, store.TransitionTask(plan.ID, "review"))
	require.NoError(t, store.TransitionTask(plan.ID, "done"))
	_, err = planstart.Start(ctx, store, mgr, plan.ID, planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrPlanWrongStatus)
}
