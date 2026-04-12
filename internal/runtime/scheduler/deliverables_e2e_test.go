package scheduler_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// TestDeliverablesE2ERequiredPresent verifies that a task with a single
// required deliverable transitions to "done" when the mock executor produces
// the required artifact.
func TestDeliverablesE2ERequiredPresent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	dir := t.TempDir()
	q, err := queue.Open(filepath.Join(dir, "queue.db"))
	require.NoError(t, err)

	mock := executor.NewMockExecutor()
	mock.SetResult(&executor.ExecutionResult{
		Status: "done",
		Artifacts: []executor.Artifact{
			{Type: "diff", Content: "--- a/file\n+++ b/file"},
		},
	})

	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:         1,
		IntervalSeconds: 1,
		RetryBudget:     3,
		Enabled:         true,
		StaleSeconds:    300,
	}

	sched := scheduler.New(store, q, registry, cfg)
	defer func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	}()

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:          "CW-20260411-E001",
		Title:       "Deliverables required present",
		Description: "Mock produces the required diff",
		Status:      "todo",
		Priority:    1,
		Executor:    "mock",
		OnDone:      "close",
		MaxRetries:  3,
		Deliverables: sql.NullString{
			String: `[{"type":"diff","required":true}]`,
			Valid:  true,
		},
	}))

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(300 * time.Millisecond)
	sched.DrainResults()

	task, err := store.GetTask("CW-20260411-E001")
	require.NoError(t, err)
	assert.Equal(t, "done", task.Status, "task should be done — required deliverable produced")
	assert.Empty(t, task.BlockedReason, "no blocked reason expected on success")
}

// TestDeliverablesE2ERequiredMissing verifies that a task with a single
// required deliverable transitions to "blocked" when the mock executor fails
// to produce it. MaxRetries=0 forces an immediate block on first attempt.
func TestDeliverablesE2ERequiredMissing(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	dir := t.TempDir()
	q, err := queue.Open(filepath.Join(dir, "queue.db"))
	require.NoError(t, err)

	mock := executor.NewMockExecutor()
	mock.SetResult(&executor.ExecutionResult{
		Status:    "done",
		Artifacts: []executor.Artifact{},
	})

	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:         1,
		IntervalSeconds: 1,
		RetryBudget:     3,
		Enabled:         true,
		StaleSeconds:    300,
	}

	sched := scheduler.New(store, q, registry, cfg)
	defer func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	}()

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:          "CW-20260411-E002",
		Title:       "Deliverables required missing",
		Description: "Mock omits the required diff",
		Status:      "todo",
		Priority:    1,
		Executor:    "mock",
		OnDone:      "close",
		OnFail:      "block",
		Deliverables: sql.NullString{
			String: `[{"type":"diff","required":true}]`,
			Valid:  true,
		},
	}))
	// applyDefaults coerces MaxRetries=0 to 3; force it back to 0 via direct SQL
	// so the first missing-deliverables failure goes straight to blocked.
	_, err = store.DB().Exec("UPDATE tasks SET max_retries = 0 WHERE id = ?", "CW-20260411-E002")
	require.NoError(t, err)

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(300 * time.Millisecond)
	sched.DrainResults()

	task, err := store.GetTask("CW-20260411-E002")
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status, "task should be blocked — required deliverable missing")
	assert.Contains(t, task.BlockedReason, "missing required deliverables",
		"blocked reason should mention missing required deliverables")
}

// TestDeliverablesE2ETwoRequiredOneMissing verifies that a task with two
// required deliverables transitions to "blocked" when only one is produced.
// MaxRetries=0 forces an immediate block on first attempt.
func TestDeliverablesE2ETwoRequiredOneMissing(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	dir := t.TempDir()
	q, err := queue.Open(filepath.Join(dir, "queue.db"))
	require.NoError(t, err)

	mock := executor.NewMockExecutor()
	mock.SetResult(&executor.ExecutionResult{
		Status: "done",
		Artifacts: []executor.Artifact{
			{Type: "diff", Content: "--- a/file\n+++ b/file"},
		},
	})

	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:         1,
		IntervalSeconds: 1,
		RetryBudget:     3,
		Enabled:         true,
		StaleSeconds:    300,
	}

	sched := scheduler.New(store, q, registry, cfg)
	defer func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	}()

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:          "CW-20260411-E003",
		Title:       "Two required, one missing",
		Description: "Mock produces diff but not test-results",
		Status:      "todo",
		Priority:    1,
		Executor:    "mock",
		OnDone:      "close",
		OnFail:      "block",
		Deliverables: sql.NullString{
			String: `[{"type":"diff","required":true},{"type":"test-results","required":true}]`,
			Valid:  true,
		},
	}))
	// applyDefaults coerces MaxRetries=0 to 3; force it back to 0 via direct SQL
	// so the first missing-deliverables failure goes straight to blocked.
	_, err = store.DB().Exec("UPDATE tasks SET max_retries = 0 WHERE id = ?", "CW-20260411-E003")
	require.NoError(t, err)

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(300 * time.Millisecond)
	sched.DrainResults()

	task, err := store.GetTask("CW-20260411-E003")
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status, "task should be blocked — one required deliverable missing")
	assert.Contains(t, task.BlockedReason, "missing required deliverables",
		"blocked reason should mention missing required deliverables")
}

// TestDeliverablesE2ERequiredAndOptional verifies that a task with a required
// and an optional deliverable transitions to "done" when only the required one
// is produced. Optional-missing must NOT block the task.
func TestDeliverablesE2ERequiredAndOptional(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	dir := t.TempDir()
	q, err := queue.Open(filepath.Join(dir, "queue.db"))
	require.NoError(t, err)

	mock := executor.NewMockExecutor()
	mock.SetResult(&executor.ExecutionResult{
		Status: "done",
		Artifacts: []executor.Artifact{
			{Type: "diff", Content: "--- a/file\n+++ b/file"},
		},
	})

	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:         1,
		IntervalSeconds: 1,
		RetryBudget:     3,
		Enabled:         true,
		StaleSeconds:    300,
	}

	sched := scheduler.New(store, q, registry, cfg)
	defer func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	}()

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:          "CW-20260411-E004",
		Title:       "Required + optional, optional missing",
		Description: "Mock produces diff; pr-link is optional and absent",
		Status:      "todo",
		Priority:    1,
		Executor:    "mock",
		OnDone:      "close",
		MaxRetries:  3,
		Deliverables: sql.NullString{
			String: `[{"type":"diff","required":true},{"type":"pr-link","required":false}]`,
			Valid:  true,
		},
	}))

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(300 * time.Millisecond)
	sched.DrainResults()

	task, err := store.GetTask("CW-20260411-E004")
	require.NoError(t, err)
	assert.Equal(t, "done", task.Status,
		"task should be done — required produced, optional-missing must not block")
	assert.Empty(t, task.BlockedReason, "no blocked reason expected when only optional is missing")
}
