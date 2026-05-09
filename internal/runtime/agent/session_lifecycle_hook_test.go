package agent

import (
	"context"
	"database/sql"
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
	mu          sync.Mutex
	getStatus   Status         // returned for every Get call
	getErr      error          // when non-nil, Get returns this
	stopErr     error          // when non-nil, Stop returns this
	stopCalls   atomic.Int32
	stopCalled  []string
	getCalled   []string
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
