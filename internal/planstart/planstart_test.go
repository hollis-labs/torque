package planstart_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	runEvents   []*sqlstore.RunEventRecord
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

func (s *stubStore) AppendRunEvent(evt *sqlstore.RunEventRecord) (int64, error) {
	s.runEvents = append(s.runEvents, evt)
	return int64(len(s.runEvents)), nil
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
}

func (s *stubMgr) Boot(_ context.Context, opts agent.Options) (*agent.Session, error) {
	s.bootCalls++
	s.lastOpts = opts
	if s.bootErr != nil {
		return nil, s.bootErr
	}
	return s.bootSession, nil
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
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-NEW"}}

	res, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-004", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrAlreadyOrchestrating)
	require.NotNil(t, res)
	assert.Equal(t, "SES-LIVE", res.SessionID)
	assert.Zero(t, mgr.bootCalls, "no fresh boot fires while live session exists")
	assert.Empty(t, store.transitions, "no transition fires on idempotent path")
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

// Redispatch cold-boot recovery: a terminal prior orchestrator session with a
// stream trace yields a recovery pack threaded into the fresh boot's
// SystemPrompt, a recovery.md pointer in the new boot dir, and a
// recovery.pack_planted run_events breadcrumb. This is torque's analog of
// nanite's cold-boot-with-prior-history recovery pack.
func TestPlanstart_RedispatchPlantsRecoveryPack(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-REC"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-REC", Kind: "plan", Status: "doing", WorkingDir: "/tmp/plan",
		Metadata: sql.NullString{
			String: `{"plan":{"orchestrator_session_id":"SES-DEAD"}}`,
			Valid:  true,
		},
	}

	// Prior orchestrator session: terminal, with a stream.jsonl trace under its
	// recorded workspace dir. The meta key torque.workspace_dir is the
	// substrate-internal convention agent.Boot stamps.
	priorWS := t.TempDir()
	logDir := filepath.Join(priorWS, "logs")
	require.NoError(t, os.MkdirAll(logDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "stream.jsonl"),
		[]byte(strings.Join([]string{
			`{"type":"delta","content":"merged PR #7, advancing to phase 2"}`,
			`{"type":"tool_use","tool_use":{"name":"torque_task_transition"}}`,
		}, "\n")+"\n"), 0o644))
	store.sessions["SES-DEAD"] = &sqlstore.SessionRecord{
		ID:       "SES-DEAD",
		Provider: "claude",
		State:    "crashed",
		MetaJSON: `{"torque.workspace_dir":"` + priorWS + `","role":"orchestrator"}`,
	}

	// Fresh boot lands in a known bootDir so recovery.md is written there.
	newBootDir := t.TempDir()
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-RESUMED", BootDir: newBootDir}}

	res, err := planstart.Redispatch(context.Background(), store, mgr, "CW-PLAN-REC", planstart.Options{})
	require.NoError(t, err)
	assert.Equal(t, "SES-RESUMED", res.SessionID)

	// Recovery pack threaded into the fresh boot's SystemPrompt (lands in boot.md).
	assert.Contains(t, mgr.lastOpts.SystemPrompt, "<recovered-session-context>")
	assert.Contains(t, mgr.lastOpts.SystemPrompt, "merged PR #7, advancing to phase 2")
	assert.Contains(t, mgr.lastOpts.SystemPrompt, "SES-DEAD")
	// The orchestrator system prompt still follows the recovery block.
	recIdx := strings.Index(mgr.lastOpts.SystemPrompt, "<recovered-session-context>")
	endIdx := strings.Index(mgr.lastOpts.SystemPrompt, "</recovered-session-context>")
	require.GreaterOrEqual(t, recIdx, 0)
	require.Greater(t, endIdx, recIdx)

	// recovery.md pointer written into the new boot dir.
	b, err := os.ReadFile(filepath.Join(newBootDir, "recovery.md"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "<recovered-session-context>")

	// recovery.pack_planted breadcrumb emitted on the plan.
	var found *sqlstore.RunEventRecord
	for _, e := range store.runEvents {
		if e.Type == "recovery.pack_planted" {
			found = e
		}
	}
	require.NotNil(t, found, "recovery.pack_planted breadcrumb must be emitted")
	assert.Equal(t, "CW-PLAN-REC", found.TaskID)
	assert.Contains(t, found.Payload, "SES-RESUMED")
	assert.Contains(t, found.Payload, "SES-DEAD")
}

// Redispatch with no prior session trace (brand-new orchestration / no recorded
// workspace) skips the recovery pack entirely — a clean cold boot.
func TestPlanstart_RedispatchNoPriorHistorySkipsRecovery(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-REC2"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-REC2", Kind: "plan", Status: "doing", WorkingDir: "/tmp/plan",
		Metadata: sql.NullString{
			String: `{"plan":{"orchestrator_session_id":"SES-EMPTY"}}`,
			Valid:  true,
		},
	}
	store.sessions["SES-EMPTY"] = &sqlstore.SessionRecord{
		ID: "SES-EMPTY", State: "crashed", MetaJSON: "{}",
	}
	mgr := &stubMgr{bootSession: &agent.Session{ID: "SES-NEW2", BootDir: t.TempDir()}}

	_, err := planstart.Redispatch(context.Background(), store, mgr, "CW-PLAN-REC2", planstart.Options{})
	require.NoError(t, err)
	assert.NotContains(t, mgr.lastOpts.SystemPrompt, "<recovered-session-context>")
	for _, e := range store.runEvents {
		assert.NotEqual(t, "recovery.pack_planted", e.Type, "no recovery breadcrumb without prior history")
	}
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
