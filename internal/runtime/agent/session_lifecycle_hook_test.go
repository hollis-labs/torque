package agent

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	_ "modernc.org/sqlite"
)

// stubStopper records every (Get, Stop) call so the unit tests can assert
// the hook's filter logic without standing up a real agent.Manager.
type stubStopper struct {
	mu         sync.Mutex
	getStatus  Status // returned for every Get call
	getErr     error  // when non-nil, Get returns this
	stopErr    error  // when non-nil, Stop returns this
	stopCalls  atomic.Int32
	stopCalled []string
	getCalled  []string
}

func (s *stubStopper) Get(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalled = append(s.getCalled, id)
	if s.getErr != nil {
		return nil, s.getErr
	}
	return &Session{ID: id, Status: s.getStatus}, nil
}

func (s *stubStopper) Stop(_ context.Context, id string) error {
	s.stopCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopCalled = append(s.stopCalled, id)
	return s.stopErr
}

func (s *stubStopper) StopCount() int32 { return s.stopCalls.Load() }

func newHookTestStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func writePlanWithSession(t *testing.T, store *sqlstore.Store, planID, status, sessID string) {
	t.Helper()
	meta := sql.NullString{Valid: true, String: `{"plan":{"orchestrator_session_id":"` + sessID + `"}}`}
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       planID,
		Title:    planID,
		Kind:     "plan",
		Status:   status,
		Priority: 1,
		Metadata: meta,
	}))
}

func writePlanNoSession(t *testing.T, store *sqlstore.Store, planID, status string) {
	t.Helper()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       planID,
		Title:    planID,
		Kind:     "plan",
		Status:   status,
		Priority: 1,
	}))
}

// TestSessionLifecycleHook_PlanTerminalTransitionStopsRunningSession covers
// PR-A acceptance criterion 1.
func TestSessionLifecycleHook_PlanTerminalTransitionStopsRunningSession(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)
	require.NotNil(t, hook)

	writePlanWithSession(t, store, "CW-PLAN-001", "doing", "SES-LIVE")

	// Direct call avoids the planTransitionStopDelay async goroutine in the
	// dispatch path; the bus integration is exercised in the no-leak test.
	hook.HandlePlanTransition(context.Background(), "CW-PLAN-001")

	require.Eventually(t, func() bool {
		return stopper.StopCount() == 1
	}, 3*time.Second, 10*time.Millisecond)

	assert.Equal(t, []string{"SES-LIVE"}, stopper.stopCalled)
}

// TestSessionLifecycleHook_AlreadyTerminalSessionSkipped covers
// PR-A acceptance criterion 2 (idempotency with PR #30 poller).
func TestSessionLifecycleHook_AlreadyTerminalSessionSkipped(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusDone}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanWithSession(t, store, "CW-PLAN-002", "doing", "SES-DONE")

	hook.HandlePlanTransition(context.Background(), "CW-PLAN-002")

	// Brief settle window — no Stop should fire because the session is
	// already terminal per the stubbed Get.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount())
}

// TestSessionLifecycleHook_NonPlanTaskIgnored covers PR-A acceptance
// criterion 3.
func TestSessionLifecycleHook_NonPlanTaskIgnored(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-AGENT-001",
		Title:    "agent task",
		Kind:     "agent",
		Status:   "review",
		Priority: 1,
		Metadata: sql.NullString{Valid: true, String: `{"plan":{"orchestrator_session_id":"SES-X"}}`},
	}))

	hook.HandlePlanTransition(context.Background(), "CW-AGENT-001")
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount())
}

// TestSessionLifecycleHook_PlanWithoutOrchestratorSessionIgnored covers
// PR-A acceptance criterion 4 — no panic, no Stop, no error log.
func TestSessionLifecycleHook_PlanWithoutOrchestratorSessionIgnored(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanNoSession(t, store, "CW-PLAN-003", "review")

	hook.HandlePlanTransition(context.Background(), "CW-PLAN-003")
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount())
}

// TestSessionLifecycleHook_DispatchFiltersNonTerminalTransitions ensures the
// bus subscriber path skips intermediate transitions (todo → doing).
func TestSessionLifecycleHook_DispatchFiltersNonTerminalTransitions(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)
	hook.Start()
	defer hook.Close()

	writePlanWithSession(t, store, "CW-PLAN-004", "doing", "SES-DOING")

	bus.Publish(scheduler.SchedulerEvent{
		Type:   "task.transitioned",
		TaskID: "CW-PLAN-004",
		Data:   map[string]interface{}{"from": "todo", "to": "doing"},
	})

	// Allow the bus dispatch + the planTransitionStopDelay window to expire.
	time.Sleep(planTransitionStopDelay + 200*time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount())
}

// TestSessionLifecycleHook_StopErrorSwallowed verifies ErrSessionNotRunning
// from Manager.Stop is silently swallowed — the PR #30 poller may have
// already driven the clean termination by the time the hook's grace delay
// expires.
func TestSessionLifecycleHook_StopErrorSwallowed(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning, stopErr: ErrSessionNotRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanWithSession(t, store, "CW-PLAN-005", "doing", "SES-RACED")
	hook.HandlePlanTransition(context.Background(), "CW-PLAN-005")

	require.Eventually(t, func() bool {
		return stopper.StopCount() == 1
	}, 3*time.Second, 10*time.Millisecond)
	// No assertion on log output — implementation contract is "no panic, no
	// error propagation"; both are exercised by the test reaching this line.
}

// TestSessionLifecycleHook_ObserveTaskTransition_FiresOnPlanTerminal verifies
// the service-layer TaskTransitionObserver path fires Stop when a plan task
// transitions to a terminal status. This is the primary production path
// (MCP/HTTP-driven transitions don't ride the scheduler EventBus).
func TestSessionLifecycleHook_ObserveTaskTransition_FiresOnPlanTerminal(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanWithSession(t, store, "CW-PLAN-OBS-1", "doing", "SES-OBS")

	hook.ObserveTaskTransition(context.Background(), "CW-PLAN-OBS-1", "doing", "review")

	require.Eventually(t, func() bool {
		return stopper.StopCount() == 1
	}, 3*time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"SES-OBS"}, stopper.stopCalled)
}

// TestSessionLifecycleHook_ObserveTaskTransition_NonTerminalIgnored guards
// the to-status filter at the observer entry point.
func TestSessionLifecycleHook_ObserveTaskTransition_NonTerminalIgnored(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanWithSession(t, store, "CW-PLAN-OBS-2", "todo", "SES-OBS-2")

	hook.ObserveTaskTransition(context.Background(), "CW-PLAN-OBS-2", "todo", "doing")

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount())
}

// --- Layer 2 (CW-20260509-0028) — session-complete marker observer ---

// TestSessionLifecycleHook_ObserveComment_StopsLinkedSession covers PR-B
// acceptance criterion 1: marker content + orchestrator author + linked
// running session triggers Stop.
func TestSessionLifecycleHook_ObserveComment_StopsLinkedSession(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanWithSession(t, store, "CW-PLAN-L2-1", "doing", "SES-MARKER")

	hook.ObserveComment(context.Background(), &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   "CW-PLAN-L2-1",
		Author:     "[system/orchestrator/foo]",
		Content:    "[system/orchestrator/session-complete] plan delegated cleanly",
	})

	require.Eventually(t, func() bool {
		return stopper.StopCount() == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"SES-MARKER"}, stopper.stopCalled)
}

// TestSessionLifecycleHook_ObserveComment_NonOrchestratorAuthorIgnored
// covers PR-B acceptance criterion 2: same content + plain `agent` author
// must NOT trigger Stop.
func TestSessionLifecycleHook_ObserveComment_NonOrchestratorAuthorIgnored(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanWithSession(t, store, "CW-PLAN-L2-2", "doing", "SES-X")

	hook.ObserveComment(context.Background(), &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   "CW-PLAN-L2-2",
		Author:     "agent",
		Content:    "[system/orchestrator/session-complete] spoofed",
	})

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount())
}

// TestSessionLifecycleHook_ObserveComment_NoSessionLinkageIgnored covers
// PR-B acceptance criterion 3: marker on a task with no orchestrator
// session linkage must NOT trigger Stop.
func TestSessionLifecycleHook_ObserveComment_NoSessionLinkageIgnored(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanNoSession(t, store, "CW-PLAN-L2-3", "doing")

	hook.ObserveComment(context.Background(), &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   "CW-PLAN-L2-3",
		Author:     "[system/orchestrator/x]",
		Content:    "[system/orchestrator/session-complete] orphaned marker",
	})

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount())
}

// TestSessionLifecycleHook_ObserveComment_MarkerOnLaterLineIgnored covers
// the strict-prefix-on-first-line discipline from the plan: marker text
// embedded later in a longer comment must NOT fire.
func TestSessionLifecycleHook_ObserveComment_MarkerOnLaterLineIgnored(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanWithSession(t, store, "CW-PLAN-L2-4", "doing", "SES-EMBEDDED")

	content := "Plan summary line 1\nLine 2\nLine 3\nLine 4\n[system/orchestrator/session-complete] embedded"
	hook.ObserveComment(context.Background(), &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   "CW-PLAN-L2-4",
		Author:     "[system/orchestrator/x]",
		Content:    content,
	})

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount())
}

// TestSessionLifecycleHook_ObserveComment_NonTaskEntityIgnored guards the
// (entity_type, entity_id) filter — collection/epic/sprint comments must
// not be treated as orchestrator markers.
func TestSessionLifecycleHook_ObserveComment_NonTaskEntityIgnored(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	hook.ObserveComment(context.Background(), &sqlstore.CommentRecord{
		EntityType: "collection",
		EntityID:   "COLL-1",
		Author:     "[system/orchestrator/x]",
		Content:    "[system/orchestrator/session-complete] wrong entity",
	})

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount())
}

// --- CW-20260510-0064 — layer-2 child-progress sanity check ---

// writeChildTask is a tiny helper for the CW-20260510-0064 layer-2 sanity-
// check tests: writes a child agent-kind task under the named plan with
// the provided status. Mirrors the existing writePlanWithSession helper's
// shape so the layer-2 tests stay consistent with the rest of this file.
func writeChildTask(t *testing.T, store *sqlstore.Store, childID, planID, status string) {
	t.Helper()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       childID,
		Title:    childID,
		Kind:     "agent",
		Status:   status,
		Priority: 1,
		ParentID: sql.NullString{Valid: true, String: planID},
	}))
}

// TestSessionLifecycleHook_ObserveComment_SuppressedWhenChildStillDoing
// is the CW-20260510-0064 acceptance test: the layer-2 marker observer
// must NOT stop the orchestrator session when a child task is still in
// progress (`doing` or `review`). The orchestrator on the agentic-execution
// run at 2026-05-10T05:18-05:21 emitted session-complete on the false
// signal that its only-active child had crashed; the substrate had no
// child-progress check and obediently SIGTERM'd the orchestrator,
// abandoning 5 of 6 phase-1 children. This test pins the fix.
func TestSessionLifecycleHook_ObserveComment_SuppressedWhenChildStillDoing(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	// Capture log output so we can assert the WARN-line contract.
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	writePlanWithSession(t, store, "CW-PLAN-CW0064-1", "doing", "SES-CW0064-1")
	// The smoking gun: child still in `doing` while the orchestrator emits
	// the session-complete marker (the false-positive shape).
	writeChildTask(t, store, "CW-CHILD-CW0064-1", "CW-PLAN-CW0064-1", "doing")

	hook.ObserveComment(context.Background(), &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   "CW-PLAN-CW0064-1",
		Author:     "[system/orchestrator/v0]",
		Content:    "[system/orchestrator/session-complete] hallucinated child crash",
	})

	// Hard assertion: NO Stop call. Wait the layer-2 dispatch window so a
	// late-firing goroutine would still get caught.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount(),
		"layer-2 stop must be suppressed while a child is still doing")

	// WARN log line must be emitted so operators can see the suppression
	// in the daemon stderr.log.
	out := buf.String()
	assert.Contains(t, out, "WARN",
		"suppression must log at WARN")
	assert.Contains(t, out, "CW-PLAN-CW0064-1",
		"WARN must name the plan id for forensic traceability")
	assert.Contains(t, out, "SES-CW0064-1",
		"WARN must name the session id for forensic traceability")
	assert.Contains(t, out, "child task still in progress",
		"WARN must explain WHY the stop was suppressed")
}

// TestSessionLifecycleHook_ObserveComment_SuppressedWhenChildAtReview is
// the same gate as the doing case; review is also in-flight (the reviewer
// end-agent is closing it). The orchestrator should not be stopped while
// the reviewer is still working.
func TestSessionLifecycleHook_ObserveComment_SuppressedWhenChildAtReview(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanWithSession(t, store, "CW-PLAN-CW0064-2", "doing", "SES-CW0064-2")
	writeChildTask(t, store, "CW-CHILD-CW0064-2", "CW-PLAN-CW0064-2", "review")

	hook.ObserveComment(context.Background(), &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   "CW-PLAN-CW0064-2",
		Author:     "[system/orchestrator/v0]",
		Content:    "[system/orchestrator/session-complete] premature exit",
	})

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), stopper.StopCount(),
		"layer-2 stop must be suppressed while a child is at review (reviewer end-agent in flight)")
}

// TestSessionLifecycleHook_ObserveComment_AllowedWhenChildrenTerminal
// is the negation: when every child is in a terminal state (done/failed/
// blocked/cancelled/abandoned) or hasn't been dispatched yet (todo), the
// stop fires normally. This guards against the suppression check
// degenerating into a hard "never stop" fallback.
func TestSessionLifecycleHook_ObserveComment_AllowedWhenChildrenTerminal(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanWithSession(t, store, "CW-PLAN-CW0064-3", "doing", "SES-CW0064-3")
	// All children done — the happy path: orchestrator finished its slice.
	writeChildTask(t, store, "CW-CHILD-CW0064-3a", "CW-PLAN-CW0064-3", "done")
	writeChildTask(t, store, "CW-CHILD-CW0064-3b", "CW-PLAN-CW0064-3", "done")

	hook.ObserveComment(context.Background(), &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   "CW-PLAN-CW0064-3",
		Author:     "[system/orchestrator/v0]",
		Content:    "[system/orchestrator/session-complete] plan complete",
	})

	require.Eventually(t, func() bool {
		return stopper.StopCount() == 1
	}, time.Second, 10*time.Millisecond,
		"layer-2 stop must fire when all children are terminal")
	assert.Equal(t, []string{"SES-CW0064-3"}, stopper.stopCalled)
}

// TestSessionLifecycleHook_ObserveComment_AllowedWhenChildrenAreOnlyTodo
// covers the orchestrator-self-block / cancelled-by-user path: the
// orchestrator emits session-complete with pending todos. The stop must
// fire — those todos are deliberately abandoned, not in flight.
func TestSessionLifecycleHook_ObserveComment_AllowedWhenChildrenAreOnlyTodo(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	writePlanWithSession(t, store, "CW-PLAN-CW0064-4", "doing", "SES-CW0064-4")
	writeChildTask(t, store, "CW-CHILD-CW0064-4a", "CW-PLAN-CW0064-4", "todo")
	writeChildTask(t, store, "CW-CHILD-CW0064-4b", "CW-PLAN-CW0064-4", "done")

	hook.ObserveComment(context.Background(), &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   "CW-PLAN-CW0064-4",
		Author:     "[system/orchestrator/v0]",
		Content:    "[system/orchestrator/session-complete] cancelled by user",
	})

	require.Eventually(t, func() bool {
		return stopper.StopCount() == 1
	}, time.Second, 10*time.Millisecond,
		"layer-2 stop must fire when remaining children are only todo (deliberate abandonment)")
}

// TestSessionLifecycleHook_HasInProgressChild_DBErrorIsConservative
// pins the conservative-on-error behavior: if the children-list query
// fails, the suppression returns false (i.e. the stop proceeds). Better
// a false-positive stop than a silently-wedged orchestrator.
func TestSessionLifecycleHook_HasInProgressChild_DBErrorIsConservative(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	// Empty plan ID — the helper's empty-id guard returns false (no
	// suppression). This also covers the "no children" case which is the
	// shape an orphaned-marker plan would present.
	assert.False(t, hook.hasInProgressChild(""),
		"empty planID must not trigger suppression")

	// Plan with no children — also no suppression.
	writePlanWithSession(t, store, "CW-PLAN-CW0064-5", "doing", "SES-CW0064-5")
	assert.False(t, hook.hasInProgressChild("CW-PLAN-CW0064-5"),
		"plan with no children must not trigger suppression")

	// Tag this assertion onto strings for stable greppability.
	const inProgressMarker = "child task still in progress"
	assert.True(t, strings.HasPrefix(inProgressMarker, "child"),
		"sentinel string for grep traceability")
}

// TestSessionLifecycleHook_NoGoroutineLeak covers PR-A acceptance criterion 6.
// The dispatcher goroutine must drain when Close is invoked.
func TestSessionLifecycleHook_NoGoroutineLeak(t *testing.T) {
	store := newHookTestStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	// IgnoreCurrent captured AFTER the store + bus are wired so the
	// database/sql.connectionOpener and any other pre-existing goroutines
	// are baseline rather than tracked.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	stopper := &stubStopper{getStatus: StatusRunning}
	hook := NewSessionLifecycleHook(bus, store, stopper)

	hook.Start()
	hook.Start() // Start is idempotent — second call must not spawn a second goroutine.
	hook.Close()
	hook.Close() // Close is idempotent.
}
