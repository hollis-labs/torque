package scheduler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// setupE2ESchedulerStack builds the full stack needed to exercise a template
// → instantiate → scheduler → mock-executor run through all real components
// (no scheduler-harness shim). Returns everything tests might want to poke.
func setupE2ESchedulerStack(t *testing.T) (*scheduler.Scheduler, *sqlstore.Store, *service.Service, *executor.MockExecutor) {
	t.Helper()

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
	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:          2,
		IntervalSeconds:  1,
		RetryBudget:      3,
		HeartbeatSeconds: 15,
		StaleSeconds:     300,
		Enabled:          true,
	}
	sched := scheduler.New(store, q, registry, nil, cfg)
	svc := service.New(store)

	t.Cleanup(func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	})

	return sched, store, svc, mock
}

// TestE2E_Template_Instantiate_Scheduler_PicksUp_Executes covers the Phase E
// exit-gate path: template create → instantiate → scheduler tick → mock
// executor runs → task transitions honor the template's on_done, and the
// task's metadata.template_ref still points at the instantiated (id, version).
func TestE2E_Template_Instantiate_Scheduler_PicksUp_Executes(t *testing.T) {
	sched, store, svc, mock := setupE2ESchedulerStack(t)

	// Mock completes with a note artifact so the deliverables gate is
	// satisfied (on_done=close → transitions to done).
	mock.SetResult(&executor.ExecutionResult{
		Status: "done",
		Artifacts: []executor.Artifact{
			{Type: "note", Content: "smoke ran"},
		},
	})

	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID:          "smoke-agent",
		Name:        "smoke",
		Description: "x",
		Kind:        "agent",
		Executor:    "mock", AgentProfile: "mock",
		AutoExecute: true,
		OnDone:      "close",
		Deliverables: []service.Deliverable{
			{Type: "note", Required: true},
		},
	})
	require.NoError(t, err)

	task, err := svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID:  "smoke-agent",
		Title:       "smoke run",
		Description: "runtime desc",
	})
	require.NoError(t, err)
	assert.Equal(t, "mock", task.Executor)
	assert.Equal(t, "agent", task.Kind)

	// CW-20260417-0133 safety override forces manual=true on every task
	// created through the service layer (HTTP / MCP / Template.Instantiate
	// / etc.). This e2e test deliberately wants the scheduler to pick the
	// task up, so flip it back to manual=false via the store — bypassing
	// the service-level override is intentional here, mirroring the
	// existing smoke-echo / serve-e2e patches.
	manualFalse := false
	require.NoError(t, store.UpdateTask(task.ID, sqlstore.TaskUpdate{Manual: &manualFalse}))

	// Scheduler tick picks it up and submits to the worker pool.
	require.NoError(t, sched.Tick(context.Background()))

	// The worker pool runs the mock executor asynchronously — wait for the
	// work to land, then drain results so the lifecycle manager transitions
	// the task.
	waitForStatus(t, sched, store, task.ID, "done", 2*time.Second)

	got, err := store.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", got.Status)

	// template_ref should survive the full round-trip.
	require.True(t, got.Metadata.Valid)
	var md map[string]any
	require.NoError(t, json.Unmarshal([]byte(got.Metadata.String), &md))
	ref := md["template_ref"].(map[string]any)
	assert.Equal(t, "smoke-agent", ref["id"])
	assert.Equal(t, float64(1), ref["version"])
}

// waitForStatus polls the store for a task to reach the target status,
// draining scheduler results each poll so the lifecycle manager can apply
// the transition.
func waitForStatus(t *testing.T, sched *scheduler.Scheduler, store *sqlstore.Store, taskID, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sched.DrainResults()
		got, err := store.GetTask(taskID)
		if err == nil && got.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := store.GetTask(taskID)
	t.Fatalf("task %s: want status %q, got %q after %s", taskID, want, got.Status, timeout)
}
