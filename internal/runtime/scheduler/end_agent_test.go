package scheduler_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// CW-20260503-0019 (S2.3) tests cover the substrate hook only — the
// reviewer LLM behavior (audit checklist + comment + transition) is
// driven by the template at runtime, not by Go code.

func setupEndAgentStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

// AC1: When a kind=agent task transitions to `review`, the lifecycle
// manager enqueues exactly one kind=internal end-agent task with the
// expected fields stamped.
func TestEndAgent_EnqueuesOnAgentReview(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	target := &sqlstore.TaskRecord{
		ID: "CW-TARGET-001", Title: "executor task", Status: "doing",
		Executor: "cli", AgentProfile: "clockwork-backend",
		Kind: "agent", OnDone: "review",
	}
	require.NoError(t, store.CreateTask(target))

	require.NoError(t, lm.HandleResult(target.ID, 1, &executor.ExecutionResult{
		Status: "done",
	}))

	// Target moved to review.
	post, err := store.GetTask(target.ID)
	require.NoError(t, err)
	assert.Equal(t, "review", post.Status)

	// Exactly one kind=internal task with parent_id=target should exist.
	internals, err := store.ListTasks(sqlstore.TaskFilter{
		Kind:     "internal",
		ParentID: target.ID,
	})
	require.NoError(t, err)
	require.Len(t, internals, 1)

	end := internals[0]
	assert.Equal(t, "internal", end.Kind)
	assert.Equal(t, scheduler.EndAgentProfile, end.AgentProfile)
	assert.Equal(t, "cli", end.Executor)
	assert.Equal(t, "todo", end.Status)
	assert.False(t, end.Manual, "end-agent must auto-dispatch (manual=false)")
	assert.Equal(t, 0, end.MaxRetries, "AC5: no retry on reviewer failure")
	assert.Equal(t, "close", end.OnDone)
	assert.Equal(t, "block", end.OnFail)
	assert.Equal(t, "agent", end.SourceType)
	assert.True(t, end.CostBudget.Valid, "AC6: separate cost budget")
	assert.Greater(t, end.CostBudget.Float64, 0.0)
	assert.Contains(t, end.Title, target.ID)
	assert.NotEmpty(t, end.SystemPrompt, "template content should be stamped")

	// metadata.end_agent records the target + template path.
	require.True(t, end.Metadata.Valid)
	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(end.Metadata.String), &meta))
	ea, ok := meta["end_agent"].(map[string]any)
	require.True(t, ok, "metadata.end_agent block should be present")
	assert.Equal(t, target.ID, ea["target_task_id"])
	assert.NotEmpty(t, ea["template"], "template path should be recorded")
}

// kind=internal must NOT recurse into another end-agent — the hook is
// gated to kind=agent only. Belt-and-suspenders against runaway loops.
func TestEndAgent_DoesNotEnqueueForInternal(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	internal := &sqlstore.TaskRecord{
		ID: "CW-INTERNAL-001", Title: "end-agent: foo", Status: "doing",
		Executor: "cli", AgentProfile: scheduler.EndAgentProfile,
		Kind: "internal", OnDone: "review",
	}
	require.NoError(t, store.CreateTask(internal))

	require.NoError(t, lm.HandleResult(internal.ID, 1, &executor.ExecutionResult{
		Status: "done",
	}))

	internals, err := store.ListTasks(sqlstore.TaskFilter{Kind: "internal"})
	require.NoError(t, err)
	assert.Len(t, internals, 1, "no second-order end-agent should fire")
}

// kind=plan / parent / wait don't run an executor — the hook should
// skip them too even when they end up in `review`.
func TestEndAgent_DoesNotEnqueueForNonAgentKinds(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	for _, kind := range []string{"plan", "parent"} {
		id := "CW-" + strings.ToUpper(kind) + "-001"
		task := &sqlstore.TaskRecord{
			ID: id, Title: kind + " task", Status: "doing",
			Executor: "cli", Kind: kind, OnDone: "review",
		}
		require.NoError(t, store.CreateTask(task))
		require.NoError(t, lm.HandleResult(id, 1, &executor.ExecutionResult{Status: "done"}))
	}

	internals, err := store.ListTasks(sqlstore.TaskFilter{Kind: "internal"})
	require.NoError(t, err)
	assert.Len(t, internals, 0, "non-agent kinds must not trigger end-agent")
}

// AC5: when a kind=internal end-agent task itself goes to `blocked`
// (executor failure, OnFail=block, MaxRetries=0), the lifecycle hook
// posts `[system/end-agent] failed: <reason>` on the parent.
func TestEndAgent_FailureCommentsOnParent(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	target := &sqlstore.TaskRecord{
		ID: "CW-TARGET-002", Title: "executor task", Status: "review",
		Executor: "cli", AgentProfile: "clockwork-backend",
		Kind: "agent",
	}
	require.NoError(t, store.CreateTask(target))

	endAgent := &sqlstore.TaskRecord{
		ID: "CW-ENDAGENT-002", Title: "end-agent: " + target.ID, Status: "doing",
		Executor: "cli", AgentProfile: scheduler.EndAgentProfile,
		Kind: "internal", OnFail: "block", MaxRetries: 0,
		ParentID: sql.NullString{String: target.ID, Valid: true},
	}
	require.NoError(t, store.CreateTask(endAgent))

	// Drive a failure that lands as `blocked` (retries exhausted at 0/0).
	require.NoError(t, lm.HandleResult(endAgent.ID, 1, &executor.ExecutionResult{
		Status: "failed",
		Reason: "model timeout",
	}))

	post, err := store.GetTask(endAgent.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", post.Status)

	comments, err := store.ListComments(target.ID)
	require.NoError(t, err)
	require.Len(t, comments, 1, "exactly one failure comment on target")
	assert.Equal(t, scheduler.EndAgentAuthor, comments[0].Author)
	assert.Contains(t, comments[0].Content, "failed")
	assert.Contains(t, comments[0].Content, endAgent.ID)
}

// On the success path (end-agent task → done), no failure comment fires —
// the reviewer's own MCP-driven audit comments stand on their own.
func TestEndAgent_SuccessLeavesNoFailureComment(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	target := &sqlstore.TaskRecord{
		ID: "CW-TARGET-003", Title: "executor task", Status: "review",
		Executor: "cli", AgentProfile: "clockwork-backend", Kind: "agent",
	}
	require.NoError(t, store.CreateTask(target))

	endAgent := &sqlstore.TaskRecord{
		ID: "CW-ENDAGENT-003", Title: "end-agent: " + target.ID, Status: "doing",
		Executor: "cli", AgentProfile: scheduler.EndAgentProfile,
		Kind: "internal", OnDone: "close",
		ParentID: sql.NullString{String: target.ID, Valid: true},
	}
	require.NoError(t, store.CreateTask(endAgent))

	require.NoError(t, lm.HandleResult(endAgent.ID, 1, &executor.ExecutionResult{
		Status: "done",
	}))

	post, err := store.GetTask(endAgent.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", post.Status)

	comments, err := store.ListComments(target.ID)
	require.NoError(t, err)
	assert.Empty(t, comments, "no system-failure comment on success path")
}

// Re-firing: when an executor task re-enters `review` (e.g. after the
// user re-opens it to todo and it runs again), a fresh end-agent fires.
// Two transitions = two end-agent tasks. Documented as the V1 contract.
func TestEndAgent_RefiresOnReReview(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	target := &sqlstore.TaskRecord{
		ID: "CW-TARGET-004", Title: "executor task", Status: "doing",
		Executor: "cli", AgentProfile: "clockwork-backend",
		Kind: "agent", OnDone: "review",
	}
	require.NoError(t, store.CreateTask(target))

	require.NoError(t, lm.HandleResult(target.ID, 1, &executor.ExecutionResult{
		Status: "done",
	}))

	// Manual cycle: user re-opens the target to `todo`, executor runs
	// again, finishes, transitions back to review.
	require.NoError(t, store.TransitionTask(target.ID, "todo"))
	require.NoError(t, store.TransitionTask(target.ID, "doing"))
	require.NoError(t, lm.HandleResult(target.ID, 2, &executor.ExecutionResult{
		Status: "done",
	}))

	internals, err := store.ListTasks(sqlstore.TaskFilter{
		Kind:     "internal",
		ParentID: target.ID,
	})
	require.NoError(t, err)
	assert.Len(t, internals, 2, "each review cycle gets its own end-agent")
}
