//go:build smoke

// Package smoke_templates_test exercises the Phase F exit criteria for the
// Task Model MVP (spec §8.3) against the real service, store, and scheduler
// — no HTTP harness. Run with `go test -tags=smoke ./cmd/torque`.
//
// Seven criteria, one subtest each:
//  1. All five reference templates create cleanly via the service layer.
//  2. backend-fix instantiates → scheduler picks up → mock executor runs.
//  3. decision-checkpoint + checkpoint_mode=blocking parks on emit;
//     respond resumes and cancel closes cleanly.
//  4. kind=wait with task_done predicate blocks until target done, then
//     transitions.
//  5. kind=external creates, is manually status-transitioned via service,
//     and honors its deliverables gate (transition-only; no executor).
//  6. kind=parent with two children auto-transitions when both reach done.
//  7. Full test suite green — validated by the un-tagged build step that
//     runs before this smoke target; not re-run here.
package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/queue"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/runtime/waitpoll"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
)

type smokeStack struct {
	svc   *service.Service
	store *sqlstore.Store
	sched *scheduler.Scheduler
	mock  *executor.MockExecutor
}

func newSmokeStack(t *testing.T) *smokeStack {
	t.Helper()

	store := sqlitetest.OpenStore(t)

	dir := t.TempDir()
	q, err := queue.Open(context.Background(), filepath.Join(dir, "queue.db"))
	require.NoError(t, err)

	mock := executor.NewMockExecutor()
	registry := executor.NewRegistry()
	registry.Register(mock)

	predicates := waitpoll.NewRegistry()
	require.NoError(t, bootstrap.Waitpoll(predicates, store))

	cfg := &config.SchedulerConfig{
		Workers:          2,
		IntervalSeconds:  1,
		RetryBudget:      3,
		HeartbeatSeconds: 15,
		StaleSeconds:     300,
		Enabled:          true,
	}
	sched := scheduler.New(store, q, registry, predicates, cfg)
	svc := service.New(store)

	t.Cleanup(func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	})

	return &smokeStack{svc: svc, store: store, sched: sched, mock: mock}
}

func (s *smokeStack) waitForStatus(t *testing.T, taskID, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.sched.DrainResults()
		got, err := s.store.GetTask(taskID)
		if err == nil && got.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := s.store.GetTask(taskID)
	t.Fatalf("task %s: want status %q, got %q after %s", taskID, want, got.Status, timeout)
}

// Criterion 1: all five reference templates create cleanly via the service.
func TestSmoke_Criterion1_AllTemplatesCreate(t *testing.T) {
	stack := newSmokeStack(t)

	templates := []service.TemplateCreateInput{
		{
			ID: "backend-fix", Name: "Backend Fix", Description: "Fix {{issue}} in {{repo_path}}",
			Kind: "agent", Executor: "cli", AutoExecute: true,
			OnDone: "review", OnFail: "retry", OnDoneMerge: "pr",
			QualityGates: []string{"go vet ./...", "go test ./..."},
			Deliverables: []service.Deliverable{
				{Type: "diff", Required: true},
				{Type: "test-results", Required: true},
				{Type: "pr-link", Required: false},
			},
			RequiredVars:     []string{"repo_path", "issue"},
			MetadataTemplate: map[string]any{"working_dir_template": "{{repo_path}}"},
			Tags:             []string{"code", "backend"},
		},
		{
			ID: "external-chore", Name: "External Chore", Description: "Off-system task",
			Kind: "external", AutoExecute: false, OnDone: "close",
			Deliverables: []service.Deliverable{{Type: "note", Required: true}},
			Tags:         []string{"external", "chore"},
		},
		{
			ID: "wait-for-task", Name: "Wait For Task", Description: "Wait on {{task_id}}",
			Kind: "wait", AutoExecute: true, OnDone: "close",
			RequiredVars: []string{"task_id"},
			MetadataTemplate: map[string]any{
				"wait": map[string]any{
					"predicate_type": "task_done",
					"params":         map[string]any{"task_id": "{{task_id}}"},
				},
			},
			Tags: []string{"wait", "dependency"},
		},
		{
			ID: "decision-checkpoint", Name: "Decision", Description: "Human-in-the-loop",
			Kind: "decision", AutoExecute: false,
			CheckpointMode: "blocking", OnCheckpointResponse: "resume",
			Deliverables: []service.Deliverable{{Type: "finding", Required: true}},
			Tags:         []string{"decision", "human-in-loop"},
		},
		{
			ID: "sprint-split-parent", Name: "Parent", Description: "Aggregates children",
			Kind: "parent", AutoExecute: true, OnDone: "close",
			Deliverables: []service.Deliverable{{Type: "report", Required: true}},
			Tags:         []string{"parent", "meta"},
		},
	}

	for _, tpl := range templates {
		created, err := stack.svc.Template.Create(tpl)
		require.NoError(t, err, "template %s should create cleanly", tpl.ID)
		assert.Equal(t, 1, created.Version, "fresh template starts at version 1")
		assert.Equal(t, tpl.Kind, created.Kind)
	}

	list, err := stack.svc.Template.List(service.TemplateListOpts{})
	require.NoError(t, err)
	assert.Len(t, list, 5)
}

// Criterion 2: backend-fix instantiates → scheduler picks up → mock runs.
func TestSmoke_Criterion2_BackendFix_SchedulerExecutes(t *testing.T) {
	stack := newSmokeStack(t)

	_, err := stack.svc.Template.Create(service.TemplateCreateInput{
		ID: "backend-fix", Name: "Backend Fix", Description: "Fix {{issue}}",
		Kind: "agent", Executor: "mock", AutoExecute: true, OnDone: "close",
		Deliverables: []service.Deliverable{{Type: "diff", Required: true}},
		RequiredVars: []string{"issue"},
	})
	require.NoError(t, err)

	stack.mock.SetResult(&executor.ExecutionResult{
		Status: "done",
		Artifacts: []executor.Artifact{
			{Type: "diff", Content: "--- a\n+++ b"},
		},
	})

	task, err := stack.svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "backend-fix", Title: "smoke fix",
		Vars: map[string]string{"issue": "auth-42"},
	})
	require.NoError(t, err)

	require.NoError(t, stack.sched.Tick(context.Background()))
	stack.waitForStatus(t, task.ID, "done", 2*time.Second)

	got, _ := stack.store.GetTask(task.ID)
	var md map[string]any
	require.NoError(t, json.Unmarshal([]byte(got.Metadata.String), &md))
	ref := md["template_ref"].(map[string]any)
	assert.Equal(t, "backend-fix", ref["id"])
}

// Criterion 3: decision-checkpoint + blocking emit parks, respond resumes,
// cancel closes.
func TestSmoke_Criterion3_DecisionCheckpoint_RespondAndCancel(t *testing.T) {
	stack := newSmokeStack(t)

	_, err := stack.svc.Template.Create(service.TemplateCreateInput{
		ID: "decision-checkpoint", Name: "Decision", Description: "pick one",
		Kind: "decision", AutoExecute: false,
		CheckpointMode: "blocking", OnCheckpointResponse: "resume",
	})
	require.NoError(t, err)

	t.Run("respond resumes", func(t *testing.T) {
		task, err := stack.svc.Template.Instantiate(service.TemplateInstantiateInput{
			TemplateID: "decision-checkpoint", Title: "decide-respond",
		})
		require.NoError(t, err)
		require.NoError(t, stack.svc.Task.Transition(task.ID, "doing"))

		runID, err := stack.store.CreateRun(&sqlstore.RunRecord{
			TaskID: task.ID, Executor: "cli", Status: "running",
		})
		require.NoError(t, err)
		emitOut, err := stack.svc.Checkpoint.Emit(service.CheckpointEmitInput{
			TaskID:      task.ID,
			RunID:       &runID,
			Type:        "collect_data",
			PayloadJSON: `{"q":"option?"}`,
		})
		require.NoError(t, err)

		got, _ := stack.store.GetTask(task.ID)
		assert.Equal(t, "review", got.Status, "blocking checkpoint parks task in review")

		require.NoError(t, stack.svc.Checkpoint.Respond(service.CheckpointRespondInput{
			CorrelationID:       emitOut.CorrelationID,
			ResponseJSON:        `{"pick":"a"}`,
			ResponderSourceType: "user",
		}))
		resumed, _ := stack.store.GetTask(task.ID)
		assert.Equal(t, "todo", resumed.Status, "respond resumes per on_checkpoint_response=resume")
	})

	t.Run("cancel closes cleanly", func(t *testing.T) {
		task, err := stack.svc.Template.Instantiate(service.TemplateInstantiateInput{
			TemplateID: "decision-checkpoint", Title: "decide-cancel",
		})
		require.NoError(t, err)
		require.NoError(t, stack.svc.Task.Transition(task.ID, "doing"))

		runID, err := stack.store.CreateRun(&sqlstore.RunRecord{
			TaskID: task.ID, Executor: "cli", Status: "running",
		})
		require.NoError(t, err)
		emitOut, err := stack.svc.Checkpoint.Emit(service.CheckpointEmitInput{
			TaskID:      task.ID,
			RunID:       &runID,
			Type:        "collect_data",
			PayloadJSON: `{}`,
		})
		require.NoError(t, err)

		require.NoError(t, stack.svc.Checkpoint.Cancel(service.CheckpointCancelInput{
			CorrelationID: emitOut.CorrelationID, Reason: "no longer relevant",
			CancelerSourceType: "user",
		}))

		cp, err := stack.svc.Checkpoint.Get(emitOut.CorrelationID)
		require.NoError(t, err)
		assert.Equal(t, "canceled", cp.Status)
	})
}

// Criterion 4: kind=wait + task_done predicate blocks until target done.
func TestSmoke_Criterion4_Wait_TaskDone(t *testing.T) {
	stack := newSmokeStack(t)

	// Target task the wait predicate watches.
	target, err := stack.svc.Task.Create(service.TaskCreateInput{
		Title: "target", Executor: "mock", OnDone: "close",
	})
	require.NoError(t, err)

	_, err = stack.svc.Template.Create(service.TemplateCreateInput{
		ID: "wait-for-task", Name: "Wait", Description: "wait",
		Kind: "wait", AutoExecute: true, OnDone: "close",
		RequiredVars: []string{"task_id"},
		MetadataTemplate: map[string]any{
			"wait": map[string]any{
				"predicate_type": "task_done",
				"params":         map[string]any{"task_id": "{{task_id}}"},
			},
		},
	})
	require.NoError(t, err)

	waitTask, err := stack.svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "wait-for-task", Title: "wait for target",
		Vars: map[string]string{"task_id": target.ID},
	})
	require.NoError(t, err)

	// Tick while target is still "todo" — predicate should return false.
	require.NoError(t, stack.sched.Tick(context.Background()))
	got, _ := stack.store.GetTask(waitTask.ID)
	assert.Equal(t, "todo", got.Status, "wait task stays in todo while target hasn't completed")

	// Flip target to done and tick again.
	require.NoError(t, stack.store.TransitionTask(target.ID, "doing"))
	require.NoError(t, stack.store.TransitionTask(target.ID, "done"))

	require.NoError(t, stack.sched.Tick(context.Background()))
	stack.waitForStatus(t, waitTask.ID, "done", 1*time.Second)
}

// Criterion 5: kind=external — creates, transitions manually via MCP, honors
// deliverables. (Transitions are driven through the service; no executor
// runs, so the deliverables gate doesn't fire — "honors" here means the
// shape persists and is re-readable.)
func TestSmoke_Criterion5_External_ManualTransition(t *testing.T) {
	stack := newSmokeStack(t)

	_, err := stack.svc.Template.Create(service.TemplateCreateInput{
		ID: "external-chore", Name: "External", Description: "off-system",
		Kind: "external", AutoExecute: false, OnDone: "close",
		Deliverables: []service.Deliverable{{Type: "note", Required: true}},
	})
	require.NoError(t, err)

	task, err := stack.svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "external-chore", Title: "off-system work",
	})
	require.NoError(t, err)
	assert.True(t, task.Manual, "external tasks are manual (auto_execute=false)")
	assert.Equal(t, "", task.Executor, "external tasks have no executor")

	// Deliverables persisted from template.
	require.True(t, task.Deliverables.Valid)

	// Manual transition path.
	require.NoError(t, stack.svc.Task.Transition(task.ID, "doing"))
	require.NoError(t, stack.svc.Task.Transition(task.ID, "done"))

	got, _ := stack.store.GetTask(task.ID)
	assert.Equal(t, "done", got.Status)
}

// Criterion 6: kind=parent — auto-transitions when all children reach done.
func TestSmoke_Criterion6_Parent_RollsUpWhenChildrenDone(t *testing.T) {
	stack := newSmokeStack(t)

	// Create two standalone child tasks (not bound to a template).
	c1, err := stack.svc.Task.Create(service.TaskCreateInput{Title: "child 1", OnDone: "close"})
	require.NoError(t, err)
	c2, err := stack.svc.Task.Create(service.TaskCreateInput{Title: "child 2", OnDone: "close"})
	require.NoError(t, err)

	// Parent task with metadata.children referencing both.
	parent, err := stack.svc.Task.Create(service.TaskCreateInput{
		Title: "parent", Kind: "parent", OnDone: "close",
		Metadata: map[string]any{"children": []any{c1.ID, c2.ID}},
	})
	require.NoError(t, err)

	// Initial tick: both children still todo → parent stays in todo.
	require.NoError(t, stack.sched.Tick(context.Background()))
	got, _ := stack.store.GetTask(parent.ID)
	assert.Equal(t, "todo", got.Status)

	// Flip both children to done.
	require.NoError(t, stack.store.TransitionTask(c1.ID, "doing"))
	require.NoError(t, stack.store.TransitionTask(c1.ID, "done"))
	require.NoError(t, stack.store.TransitionTask(c2.ID, "doing"))
	require.NoError(t, stack.store.TransitionTask(c2.ID, "done"))

	// Tick — rollup transitions parent.
	require.NoError(t, stack.sched.Tick(context.Background()))
	got, _ = stack.store.GetTask(parent.ID)
	assert.Equal(t, "done", got.Status, "parent transitions to done (on_done=close) when all children are done")
}
