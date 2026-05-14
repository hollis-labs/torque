package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/queue"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupPermErrorScheduler builds a scheduler + in-memory store + mock executor
// wired up with Workers=1 so tests have deterministic per-tick dispatch.
// The caller gets the pieces it needs; store close + scheduler stop are
// registered as t.Cleanup.
func setupPermErrorScheduler(t *testing.T) (*sqlstore.Store, *scheduler.Scheduler, *executor.MockExecutor) {
	t.Helper()
	store := sqlitetest.OpenStore(t)

	dir := t.TempDir()
	q, err := queue.Open(context.Background(), filepath.Join(dir, "queue.db"))
	require.NoError(t, err)

	mock := executor.NewMockExecutor()
	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:         1,
		IntervalSeconds: 1,
		RetryBudget:     3,
		Enabled:         true,
		StaleSeconds:    300,
	}
	sched := scheduler.New(store, q, registry, nil, cfg)
	t.Cleanup(func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	})
	return store, sched, mock
}

// TestSchedulerPermanentErrorBlocksWithoutRetries covers the hot path from
// CW-20260418-0010: an unknown/unloaded agent profile surfaces as a
// PermanentError from Validate(), and the scheduler must
//   - transition the task directly to "blocked"
//   - populate blocked_reason with the validation error text (including
//     the profile name so the operator can fix it)
//   - record at most 1 run row (status=blocked, error_message set), NEVER
//     the historical 3 runs rows produced by the retry loop
//   - NEVER invoke Run() — Validate failures are block-no-retry
func TestSchedulerPermanentErrorBlocksWithoutRetries(t *testing.T) {
	store, sched, mock := setupPermErrorScheduler(t)
	mock.SetValidateFunc(func(job *executor.ExecutionJob) error {
		if job.AgentProfile == "nanite-frontend" {
			return executor.NewPermanentError(
				fmt.Errorf("profile %q has neither command nor provider set", job.AgentProfile),
			)
		}
		return nil
	})

	store.CreateTask(&sqlstore.TaskRecord{
		ID:           "CW-PERM-0001",
		Title:        "Missing profile",
		Description:  "task references agent_profile that's not loaded",
		Status:       "todo",
		Priority:     1,
		Kind:         "agent",
		Executor:     "mock",
		AgentProfile: "nanite-frontend",
		OnFail:       "retry",
		MaxRetries:   3,
	})

	// Tick three times — even if the scheduler wrongly treated the
	// permanent error as transient, three ticks would exhaust the retry
	// budget and leave 3 runs rows. The assertions after prove it didn't.
	for i := 0; i < 3; i++ {
		require.NoError(t, sched.Tick(context.Background()))
		sched.DrainResults()
		time.Sleep(50 * time.Millisecond)
	}

	task, err := store.GetTask("CW-PERM-0001")
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status, "permanent validation error must block the task")
	assert.NotEmpty(t, task.BlockedReason, "blocked_reason must be set")
	assert.Contains(t, task.BlockedReason, "nanite-frontend",
		"blocked_reason must name the profile so the operator can fix it")
	assert.Contains(t, task.BlockedReason, "neither command nor provider",
		"blocked_reason must surface the validation failure text")

	// retry_count must remain zero — permanent errors don't consume retries.
	var retryCount int
	require.NoError(t, store.DB().QueryRow(
		"SELECT retry_count FROM tasks WHERE id = ?", "CW-PERM-0001",
	).Scan(&retryCount))
	assert.Equal(t, 0, retryCount, "permanent errors must not consume the retry budget")

	// At most one run row (audit): status=blocked, error_message set.
	runs, err := store.ListRuns("CW-PERM-0001")
	require.NoError(t, err)
	assert.LessOrEqual(t, len(runs), 1,
		"permanent error path must not create more than one run row; got %d", len(runs))
	if len(runs) == 1 {
		assert.Equal(t, "blocked", runs[0].Status,
			"audit run row for a permanent error must be status=blocked")
		assert.Contains(t, runs[0].ErrorMessage, "neither command nor provider")
	}

	// Run() must never have been invoked — the executor sees the validation
	// failure before any process is spawned.
	assert.Equal(t, 0, mock.RunCount(),
		"Run() must not be invoked for tasks that fail pre-dispatch validation")
}

// TestSchedulerTransientValidateErrorDoesNotBlock proves the scheduler
// narrowly classifies PermanentError. A non-permanent Validate error (e.g.
// an executor bug, transient dependency) MUST NOT immediately block —
// the existing retry taxonomy still applies. Guards against accidentally
// broadening the PermanentError scope.
func TestSchedulerTransientValidateErrorDoesNotBlock(t *testing.T) {
	store, sched, mock := setupPermErrorScheduler(t)
	// Plain errors.New — NOT wrapped in PermanentError.
	mock.SetValidateError(errors.New("transient validator hiccup"))

	store.CreateTask(&sqlstore.TaskRecord{
		ID:           "CW-PERM-0002",
		Title:        "Transient validate error",
		Description:  "should be treated as a normal failure, not block-no-retry",
		Status:       "todo",
		Priority:     1,
		Kind:         "agent",
		Executor:     "mock",
		AgentProfile: "any",
		OnFail:       "retry",
		MaxRetries:   1,
	})

	require.NoError(t, sched.Tick(context.Background()))
	sched.DrainResults()
	time.Sleep(50 * time.Millisecond)

	task, err := store.GetTask("CW-PERM-0002")
	require.NoError(t, err)
	assert.NotEqual(t, "blocked", task.Status,
		"non-permanent Validate error must not be treated as block-no-retry after a single tick")
}

// TestPickerSkipsAgentTaskWithEmptyProfile is the defense-in-depth guard:
// even with the pre-dispatch validation hook in place, a task with
// kind='agent' AND agent_profile=” must not be selected as a candidate.
// Repeated ticks must leave the task status=todo, never transitioned,
// with zero active_workers — the scheduler simply never sees it.
func TestPickerSkipsAgentTaskWithEmptyProfile(t *testing.T) {
	store, sched, mock := setupPermErrorScheduler(t)

	store.CreateTask(&sqlstore.TaskRecord{
		ID:           "CW-PERM-0003",
		Title:        "Empty profile agent task",
		Description:  "agent task with no agent_profile — picker must skip",
		Status:       "todo",
		Priority:     1,
		Kind:         "agent",
		Executor:     "mock",
		AgentProfile: "", // deliberately empty
		OnFail:       "retry",
		MaxRetries:   3,
	})

	for i := 0; i < 5; i++ {
		require.NoError(t, sched.Tick(context.Background()))
		sched.DrainResults()
		time.Sleep(25 * time.Millisecond)
	}

	task, err := store.GetTask("CW-PERM-0003")
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status,
		"kind=agent with empty agent_profile must stay todo — picker never selects it")

	// Scheduler must have zero active workers — the task never dispatched.
	assert.Equal(t, 0, sched.Status().ActiveWorkers,
		"no workers should have been engaged for a never-eligible task")

	// Neither Validate nor Run should have been invoked for this task.
	for _, j := range mock.ValidatedJobs() {
		assert.NotEqual(t, "CW-PERM-0003", j.TaskID,
			"picker must skip agent tasks with empty agent_profile before validation")
	}
	for _, j := range mock.RecordedJobs() {
		assert.NotEqual(t, "CW-PERM-0003", j.TaskID,
			"Run() must never be invoked for tasks the picker skipped")
	}

	// Zero runs rows: the task never left todo.
	runs, err := store.ListRuns("CW-PERM-0003")
	require.NoError(t, err)
	assert.Len(t, runs, 0, "no run rows should exist for a task the picker never selected")
}
