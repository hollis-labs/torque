package planstart_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/planstart"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/sessionmgr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubStore is the in-memory Store impl planstart's tests use. Tracks
// only the fields the trigger touches (GetTask / UpdateTask /
// TransitionTask / GetSession) so the test harness stays under 60
// lines without dragging the SQLite migrations through every test.
type stubStore struct {
	tasks    map[string]*sqlstore.TaskRecord
	sessions map[string]*sqlstore.SessionRecord
	updates  []sqlstore.TaskUpdate // for assertions
	transitions []string             // newStatus values, in order
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

// stubMgr captures Launch invocations and lets tests dictate what
// Launch returns. Mirrors the sessionmgr.Manager.Launch signature
// without dragging the real manager + go-agent-sessions pile in.
type stubMgr struct {
	launchID    string
	launchErr   error
	lastRequest sessionmgr.LaunchRequest
}

func (s *stubMgr) Launch(_ context.Context, req sessionmgr.LaunchRequest) (string, error) {
	s.lastRequest = req
	if s.launchErr != nil {
		return "", s.launchErr
	}
	return s.launchID, nil
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
	mgr := &stubMgr{launchID: "SES-ABC"}

	res, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-001", planstart.Options{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "SES-ABC", res.SessionID)
	assert.Equal(t, "CW-PLAN-001", res.PlanID)
	assert.False(t, res.StartedAt.IsZero())

	// LaunchRequest stamps from the orchestrator package's contract.
	assert.Equal(t, "/tmp/plan", mgr.lastRequest.Workdir)
	assert.Equal(t, "PRJ-1", mgr.lastRequest.ProjectID)
	assert.Equal(t, "CW-PLAN-001", mgr.lastRequest.TaskID)
	assert.NotEmpty(t, mgr.lastRequest.SystemPrompt)
	assert.Equal(t, "CW-PLAN-001", mgr.lastRequest.SessionMeta["plan_id"])

	// Plan transitioned + metadata stamped.
	require.Len(t, store.transitions, 1)
	assert.Equal(t, "doing", store.transitions[0])
	post, _ := store.GetTask("CW-PLAN-001")
	assert.Contains(t, post.Metadata.String, "SES-ABC")
	assert.Contains(t, post.Metadata.String, "orchestrator_session_id")
}

// AC2: starting from `review` is allowed (re-running a plan after the
// reviewer surfaced misses). Plan status is NOT moved out of review
// in that case — the orchestrator's walk handles status as it goes.
func TestPlanstart_FromReview(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-002"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-002", Kind: "plan", Status: "review", WorkingDir: "/tmp/plan",
	}
	mgr := &stubMgr{launchID: "SES-XYZ"}

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

// AC5: idempotency — already-running session returns
// ErrAlreadyOrchestrating wrapped, and the existing session id
// surfaces in Result so handlers can redirect to the live session
// view rather than show a hard error. Test uses status=todo so the
// idempotency check is reachable (status=doing fails the startable
// gate first, which is correct domain behavior — re-running a plan
// that's already executing must round-trip through review).
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
	mgr := &stubMgr{launchID: "SES-NEW"}

	res, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-004", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrAlreadyOrchestrating)
	require.NotNil(t, res)
	assert.Equal(t, "SES-LIVE", res.SessionID)
	assert.Empty(t, mgr.lastRequest.AgentProfile, "no fresh launch fires while live session exists")
	assert.Empty(t, store.transitions, "no transition fires on idempotent path")
}

// When the recorded session is stale (terminal state), drop the
// metadata reference and proceed with a fresh launch.
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
	mgr := &stubMgr{launchID: "SES-FRESH"}

	res, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-005", planstart.Options{})
	require.NoError(t, err)
	assert.Equal(t, "SES-FRESH", res.SessionID)
	assert.Equal(t, "CW-PLAN-005", mgr.lastRequest.TaskID, "fresh launch should fire")
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

// Missing workdir (Options.Workdir empty AND plan.WorkingDir empty)
// surfaces the dedicated ErrWorkdirRequired sentinel — handlers map
// it to 422 with field=workdir rather than mis-attributing to plan_id
// (PR #18 review feedback).
func TestPlanstart_WorkdirRequired(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-NOWORKDIR"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-NOWORKDIR", Kind: "plan", Status: "todo",
		// Note: no WorkingDir set.
	}
	_, err := planstart.Start(context.Background(), store, &stubMgr{}, "CW-PLAN-NOWORKDIR", planstart.Options{})
	assert.ErrorIs(t, err, planstart.ErrWorkdirRequired)
	// Must NOT be misclassified as ErrPlanNotFound.
	assert.NotErrorIs(t, err, planstart.ErrPlanNotFound)
}

// Workdir resolution: explicit Options.Workdir wins; otherwise
// plan.WorkingDir; otherwise an error.
func TestPlanstart_WorkdirOverride(t *testing.T) {
	store := newStubStore()
	store.tasks["CW-PLAN-007"] = &sqlstore.TaskRecord{
		ID: "CW-PLAN-007", Kind: "plan", Status: "todo",
		WorkingDir: "/tmp/plan-default",
	}
	mgr := &stubMgr{launchID: "SES-1"}

	_, err := planstart.Start(context.Background(), store, mgr, "CW-PLAN-007", planstart.Options{
		Workdir: "/tmp/override",
	})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/override", mgr.lastRequest.Workdir)
}
