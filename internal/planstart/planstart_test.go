package planstart_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/planstart"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubStore is the in-memory Store impl planstart's tests use. Tracks only
// the fields the trigger touches (GetTask / UpdateTask / TransitionTask /
// GetSession) so the test harness stays under 60 lines without dragging
// the SQLite migrations through every test.
type stubStore struct {
	tasks       map[string]*sqlstore.TaskRecord
	sessions    map[string]*sqlstore.SessionRecord
	updates     []sqlstore.TaskUpdate
	transitions []string
}

func newStubStore() *stubStore {
	return &stubStore{
		tasks:    map[string]*sqlstore.TaskRecord{},
		sessions: map[string]*sqlstore.SessionRecord{},
	}
}

func (s *stubStore) GetTask(id string) (*sqlstore.TaskRecord, error) {
	t, ok := s.tasks[id]
	if !ok {
		return nil, errors.New("task not found")
	}
	return t, nil
}

func (s *stubStore) UpdateTask(id string, u sqlstore.TaskUpdate) error {
	s.updates = append(s.updates, u)
	t, ok := s.tasks[id]
	if !ok {
		return errors.New("task not found")
	}
	if u.Metadata != nil {
		t.Metadata = *u.Metadata
	}
	if u.WorkingDir != nil {
		t.WorkingDir = *u.WorkingDir
	}
	return nil
}

func (s *stubStore) TransitionTask(id, newStatus string) error {
	s.transitions = append(s.transitions, newStatus)
	t, ok := s.tasks[id]
	if !ok {
		return errors.New("task not found")
	}
	t.Status = newStatus
	return nil
}

func (s *stubStore) GetSession(id string) (*sqlstore.SessionRecord, error) {
	rec, ok := s.sessions[id]
	if !ok {
		return nil, errors.New("session not found")
	}
	return rec, nil
}

// stubMgr captures Boot invocations and lets tests dictate what each
// returns. Mirrors the agent.Manager surface planstart depends on
// (Boot → *agent.Session) without dragging the real manager + go-agent-
// sessions pile in. Replaces the prior sessionmgr.Manager-shaped stub
// after CW-20260508-0001 collapsed Launch + SendInput into Boot.
type stubMgr struct {
	bootSession *agent.Session
	bootErr     error
	lastOpts    agent.Options
	bootCalls   int
	// aliveIDs is the set of session ids the stub manager considers live
	// (in-registry). Empty = nothing is live. Tests that exercise
	// Redispatch's stale-running guard (CW-20260519-0082) populate this
	// to distinguish a truly-live session from a stale row.
	aliveIDs map[string]bool
}

func (s *stubMgr) Boot(_ context.Context, opts agent.Options) (*agent.Session, error) {
	s.bootCalls++
	s.lastOpts = opts
	if s.bootErr != nil {
		return nil, s.bootErr
	}
	return s.bootSession, nil
}

// IsAlive implements planstart.SessionManager. Used by Redispatch's
// liveness check to disambiguate stale-running rows from real live
// sessions (CW-20260519-0082).
func (s *stubMgr) IsAlive(id string) bool {
	return s.aliveIDs[id]
}

// AC1+AC4: happy path — plan validates, orchestrator session boots,
// metadata stamps, plan transitions todo → doing.
func TestPlanstart_HappyPath(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-001"] = &sqlstore.TaskRecord{
		ID:         "CW-PLAN-001",
		Kind:       "plan",
		Status:     "todo",
		WorkingDir: "/tmp/plan",
		ProjectID:  sql.NullString{String: "PRJ-1", Valid: true},
	}
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-ABC"}}

	res, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-001", planstart.Options{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "SES-ABC", res.SessionID)
	assert.Equal(t, "CW-PLAN-001", res.PlanID)
	assert.False(t, res.StartedAt.IsZero())

	// agent.Options assembly stamps the orchestrator-package contract.
	assert.Equal(t, agent.ModeLongLived, mgr.lastOpts.Mode)
	assert.Equal(t, "/tmp/plan", mgr.lastOpts.Workdir)
	assert.Equal(t, "PRJ-1", mgr.lastOpts.ProjectID)
	assert.Equal(t, "CW-PLAN-001", mgr.lastOpts.TaskID)
	assert.NotEmpty(t, mgr.lastOpts.SystemPrompt)
	assert.Equal(t, "CW-PLAN-001", mgr.lastOpts.SessionMeta["plan_id"])

	// Boot fires exactly once — no separate SendInput call.
	// AutoFireFirstTurn drives the kickoff inside agent.Boot.
	assert.Equal(t, 1, mgr.bootCalls, "Boot must fire exactly once")

	// Plan transitioned + metadata stamped.
	require.Len(t, store.transitions, 1)
	assert.Equal(t, "doing", store.transitions[0])
	post, _ := store.GetTask("CW-PLAN-001")
	assert.Contains(t, post.Metadata.String, "SES-ABC")
	assert.Contains(t, post.Metadata.String, "orchestrator_session_id")
}

// Boot rollback: agent.Boot failure (Manager.Start failure, boot dir
// failure, etc) leaves the plan untouched (status=todo, no
// orchestrator_session_id metadata) so a retry from todo is the obvious
// next step. Replaces the prior "first-turn rollback" test — the
// AutoFireFirstTurn path bundles the failure into a single Boot error
// rather than the legacy Launch-then-SendInput two-step.
func TestPlanstart_BootFailureRollsBack(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-FIRSTTURN"] = &sqlstore.TaskRecord{
		ID:         "CW-PLAN-FIRSTTURN",
		Kind:       "plan",
		Status:     "todo",
		WorkingDir: "/tmp/plan",
	}
	mgr := &stubMgr{bootErr: errors.New("boom: agent.Boot failed")}

	res, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-FIRSTTURN", planstart.Options{})
	assert.Nil(t, res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boot orchestrator")

	// Plan must remain in todo with NO orchestrator metadata stamped.
	post, _ := store.GetTask("CW-PLAN-FIRSTTURN")
	assert.Equal(t, "todo", post.Status, "plan must NOT transition out of todo on Boot failure")
	assert.False(t, post.Metadata.Valid && post.Metadata.String != "" && post.Metadata.String != "{}",
		"orchestrator metadata must NOT be stamped on Boot failure; got %q", post.Metadata.String)
	assert.Empty(t, store.transitions, "no FSM transitions on Boot failure")
	assert.Empty(t, store.updates, "no metadata writes on Boot failure")
}

// AC2: starting from `review` is allowed (re-running a plan after the
// reviewer surfaced misses). Plan status is NOT moved out of review
// in that case — the orchestrator's walk handles status as it goes.
func TestPlanstart_FromReview(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-002"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-002", Kind: "plan", Status: "review", WorkingDir: "/tmp/plan",
	}
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-XYZ"}}

	_, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-002", planstart.Options{})
	require.NoError(t, err)
	// review → review (no transition) — only `todo` flips to doing.
	assert.Empty(t, store.transitions, "review status should not be moved to doing")
}

// Wrong-kind task → ErrPlanNotFound.
func TestPlanstart_WrongKind(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-NOT-PLAN"] = &sqlstore.TaskRecord{
		ID: "CW-NOT-PLAN", Kind: "agent", Status: "todo", WorkingDir: "/tmp",
	}
	_, err := planstart.Start(context.Background(), store, &stubMgr{}, "CW-NOT-PLAN", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrPlanNotFound)
}

// Missing plan → ErrPlanNotFound.
func TestPlanstart_PlanMissing(t *testing.T) {
	store := newStubStore()
	_, err := planstart.Start(context.Background(), store, &stubMgr{}, "CW-NOPE", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrPlanNotFound)
}

// Plan in non-startable status → ErrPlanWrongStatus.
func TestPlanstart_BadStatus(t *testing.T) {
	store := newStubStore()
	for _, st := range []string{"doing", "done", "blocked", "abandoned"} {
		id := "CW-PLAN-" + st
		store.tasks[id] = &sqlstore.TaskRecord{
			ID: id, Kind: "plan", Status: st, WorkingDir: "/tmp",
		}
		_, err := planstart.Start(context.Background(), store, &stubMgr{}, id, planstart.Options{})
		assert.ErrorIs(t, err, planstart.ErrPlanWrongStatus, "status=%s should be rejected", st)
	}
}

// AC5: idempotency — already-running session returns ErrAlreadyOrchestrating
// wrapped, and the existing session id surfaces in Result so handlers can
// redirect to the live session view rather than show a hard error.
//
// CW-20260519-0082: "running" alone is not enough — Start now also
// requires the session to be in the manager's live registry. Tests that
// want the idempotent path must mark the session alive on the stub.
func TestPlanstart_IdempotentBeforeNewLaunch(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-004"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-004", Kind: "plan", Status: "todo", WorkingDir: "/tmp",
		Metadata: sql.NullString{
			String: `{"plan":{"orchestrator_session_id":"SES-LIVE"}}`,
			Valid:  true,
		},
	}
	store.sessions["SES-LIVE"] = &sqlstore.SessionRecord{
		ID: "SES-LIVE", State: "running", CreatedAt: time.Now().Add(-time.Minute),
	}
	mgr := &stubMgr{
		bootSession: &agent.Session{ID: "SES-NEW"},
		aliveIDs:    map[string]bool{"SES-LIVE": true},
	}

	res, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-004", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrAlreadyOrchestrating)
	require.NotNil(t, res)
	assert.Equal(t, "SES-LIVE", res.SessionID)
	assert.Zero(t, mgr.bootCalls, "no fresh boot fires while live session exists")
	assert.Empty(t, store.transitions, "no transition fires on idempotent path")
}

// CW-20260519-0082: a session row that says `running` but whose session
// is no longer in the manager's live registry is stale — Start must
// treat it like the terminal-state case and proceed with a fresh boot
// rather than 409ing the operator with ErrAlreadyOrchestrating forever.
func TestPlanstart_StaleRunningRowRefires(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-STALE-RUNNING"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-STALE-RUNNING", Kind: "plan", Status: "todo", WorkingDir: "/tmp",
		Metadata: sql.NullString{
			String: `{"plan":{"orchestrator_session_id":"SES-STALE"}}`,
			Valid:  true,
		},
	}
	store.sessions["SES-STALE"] = &sqlstore.SessionRecord{
		ID: "SES-STALE", State: "running",
	}
	// aliveIDs is intentionally empty — the row says running but no live
	// session backs it (orchestrator session-complete was suppressed, lib
	// dropped a state write, or daemon died mid-transition).
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-FRESH"}}

	res, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-STALE-RUNNING", planstart.Options{})
	require.NoError(t, err, "stale-running row must not 409 — Start should re-boot")
	require.NotNil(t, res)
	assert.Equal(t, "SES-FRESH", res.SessionID, "fresh boot must replace the stale session id")
	assert.Equal(t, 1, mgr.bootCalls, "Boot must fire to recover from a stale running row")
}

// When the recorded session is stale (terminal state), drop the metadata
// reference and proceed with a fresh boot.
func TestPlanstart_StaleSessionRefires(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-005"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-005", Kind: "plan", Status: "todo", WorkingDir: "/tmp",
		Metadata: sql.NullString{
			String: `{"plan":{"orchestrator_session_id":"SES-CRASHED"}}`,
			Valid:  true,
		},
	}
	store.sessions["SES-CRASHED"] = &sqlstore.SessionRecord{
		ID: "SES-CRASHED", State: "crashed",
	}
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-FRESH"}}

	res, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-005", planstart.Options{})
	require.NoError(t, err)
	assert.Equal(t, "SES-FRESH", res.SessionID)
	assert.Equal(t, "CW-PLAN-005", mgr.lastOpts.TaskID, "fresh boot should fire")
}

// Nil session manager → ErrSessionMgrMissing.
func TestPlanstart_NilManager(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-006"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-006", Kind: "plan", Status: "todo", WorkingDir: "/tmp",
	}
	_, err := planstart.Start(context.Background(), store, nil, "CW-PLAN-006", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrSessionMgrMissing)
}

// Missing workdir (Options.Workdir empty AND plan.WorkingDir empty) surfaces
// the dedicated ErrWorkdirRequired sentinel — handlers map it to 422 with
// field=workdir rather than mis-attributing to plan_id.
func TestPlanstart_WorkdirRequired(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-NOWORKDIR"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-NOWORKDIR", Kind: "plan", Status: "todo",
	}
	_, err := planstart.Start(context.Background(), store, &stubMgr{}, "CW-PLAN-NOWORKDIR", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrWorkdirRequired)
	assert.NotErrorIs(t, err, planstart.ErrPlanNotFound)
}

// Workdir resolution: explicit Options.Workdir wins; otherwise plan.WorkingDir.
func TestPlanstart_WorkdirOverride(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-007"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-007", Kind: "plan", Status: "todo",
		WorkingDir: "/tmp/plan-default",
	}
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-1"}}

	_, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-007", planstart.Options{
		Workdir: "/tmp/override",
	})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/override", mgr.lastOpts.Workdir)
}

// CW-20260508-0004 persist-back: when the resolved workdir diverges from
// the stored plan.WorkingDir, planstart writes the resolved value back so
// child sub-tasks created mid-orchestration (planner / reviewer) inherit
// the correct value via the service-layer parent->child WorkingDir
// inheritance.
func TestPlanstart_PersistsResolvedWorkdirOnOverride(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-PERSIST"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-PERSIST", Kind: "plan", Status: "todo",
		WorkingDir: "/tmp/plan-default",
	}
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-1"}}

	_, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-PERSIST", planstart.Options{
		Workdir: "/tmp/override",
	})
	require.NoError(t, err)

	// Find the WorkingDir UpdateTask call (there's also a metadata update;
	// they may arrive in either order).
	var workdirUpdate *string
	for _, u := range store.updates {
		if u.WorkingDir != nil {
			workdirUpdate = u.WorkingDir
			break
		}
	}
	require.NotNil(t, workdirUpdate, "expected an UpdateTask with WorkingDir set")
	assert.Equal(t, "/tmp/override", *workdirUpdate)

	post, _ := store.GetTask("CW-PLAN-PERSIST")
	assert.Equal(t, "/tmp/override", post.WorkingDir,
		"plan.WorkingDir must be persisted back to the resolved value")
}

// When the resolved workdir matches the stored plan.WorkingDir (caller
// passes no override or passes the same value), no WorkingDir UpdateTask
// fires — the persist is conditional on divergence.
func TestPlanstart_NoPersistWhenWorkdirMatches(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-MATCH"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-MATCH", Kind: "plan", Status: "todo",
		WorkingDir: "/tmp/plan-default",
	}
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-1"}}

	_, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-MATCH", planstart.Options{})
	require.NoError(t, err)

	for _, u := range store.updates {
		assert.Nil(t, u.WorkingDir,
			"no WorkingDir UpdateTask should fire when resolved workdir matches plan.WorkingDir")
	}
}

// Plan with empty stored WorkingDir + caller-provided Options.Workdir:
// resolved workdir comes from Options, persist fires (the empty stored
// value diverges from the caller-provided one).
func TestPlanstart_PersistsWhenStoredWorkdirEmpty(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-EMPTY"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-EMPTY", Kind: "plan", Status: "todo",
		WorkingDir: "", // empty — caller must supply Options.Workdir
	}
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-1"}}

	_, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-EMPTY", planstart.Options{
		Workdir: "/tmp/from-options",
	})
	require.NoError(t, err)

	var workdirUpdate *string
	for _, u := range store.updates {
		if u.WorkingDir != nil {
			workdirUpdate = u.WorkingDir
			break
		}
	}
	require.NotNil(t, workdirUpdate, "expected an UpdateTask with WorkingDir set")
	assert.Equal(t, "/tmp/from-options", *workdirUpdate)
}

// Redispatch unit tests (CW-20260519-0082). Redispatch is the substrate-
// driven continuation of an exited orchestrator session — the runtime
// path the bootstrap.OrchestratorRedispatcher hook calls when a HITL
// checkpoint affecting a plan is responded.

// Terminal session row → Redispatch boots fresh and re-stamps the plan's
// orchestrator_session_id. The orchestrator's redispatch-preflight will
// read plan.metadata.checkpoint_responses on its first turn after boot.
func TestPlanstart_Redispatch_TerminalSessionBootsFresh(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-RD-1"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-RD-1", Kind: "plan", Status: "doing", WorkingDir: "/tmp",
		Metadata: sql.NullString{
			String: `{"plan":{"orchestrator_session_id":"SES-DONE"}}`,
			Valid:  true,
		},
	}
	store.sessions["SES-DONE"] = &sqlstore.SessionRecord{
		ID: "SES-DONE", State: "done",
	}
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-RESPAWN"}}

	res, err := planstart.Redispatch(context.Background(), store, mgr, "CW-PLAN-RD-1", planstart.Options{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "SES-RESPAWN", res.SessionID)
	assert.Equal(t, 1, mgr.bootCalls)

	// Plan metadata re-stamped with the new session id; no FSM transition
	// (Redispatch does not flip status — the plan stays doing).
	post, _ := store.GetTask("CW-PLAN-RD-1")
	assert.Contains(t, post.Metadata.String, "SES-RESPAWN")
	assert.Empty(t, store.transitions, "Redispatch must not transition the plan")
}

// Truly live session (state non-terminal AND in manager registry) →
// idempotent no-op via ErrAlreadyOrchestrating. The live orchestrator
// will pick up the checkpoint response on its next boot.
func TestPlanstart_Redispatch_LiveSessionIsIdempotent(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-RD-2"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-RD-2", Kind: "plan", Status: "doing", WorkingDir: "/tmp",
		Metadata: sql.NullString{
			String: `{"plan":{"orchestrator_session_id":"SES-LIVE-RD"}}`,
			Valid:  true,
		},
	}
	store.sessions["SES-LIVE-RD"] = &sqlstore.SessionRecord{
		ID: "SES-LIVE-RD", State: "running",
	}
	mgr := &stubMgr{
		bootSession: &agent.Session{ID: "SES-SHOULDNT-FIRE"},
		aliveIDs:    map[string]bool{"SES-LIVE-RD": true},
	}

	res, err := planstart.Redispatch(context.Background(), store, mgr, "CW-PLAN-RD-2", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrAlreadyOrchestrating)
	require.NotNil(t, res)
	assert.Equal(t, "SES-LIVE-RD", res.SessionID)
	assert.Zero(t, mgr.bootCalls, "live session must not be re-booted")
}

// Stale-running row (state=running but NOT in the manager's registry) →
// Redispatch treats the row as terminal and boots fresh. This is the
// core bug fix: bug 2 (session record stuck at running after the
// orchestrator emitted session-complete) directly produces this state,
// and without the registry cross-check Redispatch no-ops with
// ErrAlreadyOrchestrating, stalling the plan forever.
func TestPlanstart_Redispatch_StaleRunningRowBootsFresh(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-RD-3"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-RD-3", Kind: "plan", Status: "doing", WorkingDir: "/tmp",
		Metadata: sql.NullString{
			String: `{"plan":{"orchestrator_session_id":"SES-STALE-RD"}}`,
			Valid:  true,
		},
	}
	store.sessions["SES-STALE-RD"] = &sqlstore.SessionRecord{
		ID: "SES-STALE-RD", State: "running",
	}
	// aliveIDs is empty — the row says running but the manager has no
	// memory of it.
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-FRESH-RD"}}

	res, err := planstart.Redispatch(context.Background(), store, mgr, "CW-PLAN-RD-3", planstart.Options{})
	require.NoError(t, err, "stale-running row must not return ErrAlreadyOrchestrating — Redispatch must boot fresh")
	require.NotNil(t, res)
	assert.Equal(t, "SES-FRESH-RD", res.SessionID, "fresh boot must replace the stale session id")
	assert.Equal(t, 1, mgr.bootCalls)

	post, _ := store.GetTask("CW-PLAN-RD-3")
	assert.Contains(t, post.Metadata.String, "SES-FRESH-RD",
		"plan metadata must re-stamp with the new session id after the stale-row recovery")
}

// Terminal plan (done/blocked/abandoned/cancelled) → ErrPlanWrongStatus.
// Re-running a finished plan is a separate V1.1 concern.
func TestPlanstart_Redispatch_RefusesTerminalPlan(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-RD-DONE"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-RD-DONE", Kind: "plan", Status: "done", WorkingDir: "/tmp",
	}
	_, err := planstart.Redispatch(context.Background(), store, &stubMgr{}, "CW-PLAN-RD-DONE", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrPlanWrongStatus)
}
