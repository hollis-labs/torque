package scheduler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// enqueueFailWriter is a writeq.Writer that fails only the end-agent enqueue
// submit (matched by op name) and delegates everything else — notably the
// `lifecycle_transition` write — to a real Direct writer. It lets a test
// exercise the observable-failure path without also breaking the target's
// transition into `review`.
type enqueueFailWriter struct {
	inner writeq.Writer
	err   error
}

func (w *enqueueFailWriter) Submit(ctx context.Context, name string, fn func(*sqlstore.WriteTx) error) error {
	if name == "lifecycle_enqueue_end_agent" {
		return w.err
	}
	return w.inner.Submit(ctx, name, fn)
}

func (w *enqueueFailWriter) Stats() writeq.Stats { return w.inner.Stats() }

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
		Executor: "cli", AgentProfile: "torque-backend",
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
		Executor: "cli", AgentProfile: "torque-backend",
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
		Executor: "cli", AgentProfile: "torque-backend", Kind: "agent",
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

// V2 reviewer template (CW-20260519-0118 direction): the template must
// carry the new PR-aware contract — alignment check (5), design skim (6),
// follow-up filing (7), and a close-out flow that merges the PR on a
// clean audit + tags `agent-closed`. Replaces the V1 PR-merge-gate test
// (CW-20260510-0109) which pinned the V1 contract where the reviewer left
// PR-gated tasks at `review` waiting for human merge.
//
// This is a template-content assertion only. The end-agent is an LLM
// driven by the template at runtime; Go-side hooks (enqueue,
// failure-comment, retry-suppression) don't change behaviorally.
func TestEndAgent_TemplateIncludesPRAlignmentAndAutoMerge(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	target := &sqlstore.TaskRecord{
		ID: "CW-TARGET-PR-001", Title: "executor task", Status: "doing",
		Executor: "cli", AgentProfile: "torque-backend",
		Kind: "agent", OnDone: "review",
	}
	require.NoError(t, store.CreateTask(target))

	require.NoError(t, lm.HandleResult(target.ID, 1, &executor.ExecutionResult{
		Status: "done",
	}))

	internals, err := store.ListTasks(sqlstore.TaskFilter{
		Kind:     "internal",
		ParentID: target.ID,
	})
	require.NoError(t, err)
	require.Len(t, internals, 1)
	prompt := internals[0].SystemPrompt

	// V2 check 5 — PR alignment — replaces V1's check 6 PR-merge gate.
	// The reviewer now reads the diff and checks scope match; merge moves
	// to the close-out flow (only fires when the audit is clean).
	assert.Contains(t, prompt, "5. PR alignment",
		"V2 check 5 (PR alignment) must be present in the audit checklist")

	// Detection signal: artifact-shape, not agent_profile name. Carried
	// forward from V1 — real-world data uses generic profiles for
	// PR-producing roles, so name-matching would silently miss most cases.
	assert.Contains(t, prompt, "torque_artifact_list",
		"template must point the agent at the artifact list MCP tool")
	assert.Contains(t, prompt, "/pull/",
		"template must describe GitHub PR URL shape for detection")

	// Verification mechanism: gh pr view for the diff inspection.
	assert.Contains(t, prompt, "gh pr view",
		"template must specify the gh CLI inspection command")
	assert.Contains(t, prompt, "gh pr diff",
		"template must specify gh pr diff for scope verification")

	// Close-out: clean audit ⇒ apply agent-closed tag + auto-merge the PR.
	assert.Contains(t, prompt, "`agent-closed`",
		"close-out must instruct the reviewer to apply the agent-closed tag")
	assert.Contains(t, prompt, "gh pr merge",
		"close-out must instruct the reviewer to merge clean PRs")
	assert.Contains(t, prompt, "--squash",
		"close-out must default the merge strategy to squash")
	assert.Contains(t, prompt, "If the merge fails",
		"close-out must handle the merge-failure path explicitly")

	// V2 check 6 — sound design — replaces V1's no-code-review stance.
	assert.Contains(t, prompt, "6. Sound design",
		"V2 check 6 (sound design skim) must be present")
}

// V2 reviewer template enumerates the audit checks in a renumbered shape:
// status/on_done sanity, blocked_reason, agent_profile, updated_at, PR
// alignment, sound design, follow-up filing. Replaces the V1 five-check
// regression guard (CW-20260510-0109) which pinned the V1 wording. This
// test locks the V2 enumeration so future edits to the template don't
// silently drop or renumber the contract.
func TestEndAgent_TemplateEnumeratesV2Checks(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	target := &sqlstore.TaskRecord{
		ID: "CW-TARGET-PR-002", Title: "executor task", Status: "doing",
		Executor: "cli", AgentProfile: "torque-backend",
		Kind: "agent", OnDone: "review",
	}
	require.NoError(t, store.CreateTask(target))

	require.NoError(t, lm.HandleResult(target.ID, 1, &executor.ExecutionResult{
		Status: "done",
	}))

	internals, err := store.ListTasks(sqlstore.TaskFilter{
		Kind:     "internal",
		ParentID: target.ID,
	})
	require.NoError(t, err)
	require.Len(t, internals, 1)
	prompt := internals[0].SystemPrompt

	// Numbered enumeration markers — drift-detection. If the V2 checks get
	// renumbered or one is silently dropped, this test fails loudly.
	assert.Contains(t, prompt, "1. Status + on_done sanity")
	assert.Contains(t, prompt, "2. `blocked_reason` empty unless blocked")
	assert.Contains(t, prompt, "3. `agent_profile` set if kind=agent")
	assert.Contains(t, prompt, "4. `updated_at` post-dates last run")
	assert.Contains(t, prompt, "5. PR alignment")
	assert.Contains(t, prompt, "6. Sound design")
	assert.Contains(t, prompt, "7. Follow-up filing")
}

func TestEndAgent_TemplateIncludesHITLCheckpointProtocol(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	target := &sqlstore.TaskRecord{
		ID: "CW-TARGET-HITL-001", Title: "executor task", Status: "doing",
		Executor: "cli", AgentProfile: "torque-backend",
		Kind: "agent", OnDone: "review",
	}
	require.NoError(t, store.CreateTask(target))

	require.NoError(t, lm.HandleResult(target.ID, 1, &executor.ExecutionResult{
		Status: "done",
	}))

	internals, err := store.ListTasks(sqlstore.TaskFilter{
		Kind:     "internal",
		ParentID: target.ID,
	})
	require.NoError(t, err)
	require.Len(t, internals, 1)
	prompt := internals[0].SystemPrompt

	assert.Contains(t, prompt, "Redispatch preflight")
	assert.Contains(t, prompt, "target.metadata.checkpoint_responses")
	assert.Contains(t, prompt, "HITL checkpoints")
	assert.Contains(t, prompt, "torque_task_checkpoint_emit")
	assert.Contains(t, prompt, "type=\"pr_review\"")
	assert.Contains(t, prompt, "type=\"approval\"")
	assert.Contains(t, prompt, "type=\"message\"")
}

// CW-20260519-0081: when the end-agent enqueue cannot be persisted, the
// failure must be OBSERVABLE — a `[system/end-agent] failed to enqueue`
// comment on the target plus an `end_agent_enqueue_failed` run_event — so the
// orchestrator can escalate instead of stalling on a target stuck at `review`
// with no reviewer. The target's transition into `review` must still stand.
func TestEndAgent_EnqueueFailureIsObservable(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)
	lm.SetStateWriter(&enqueueFailWriter{
		inner: writeq.NewDirect(store),
		err:   errors.New("simulated store failure"),
	})

	target := &sqlstore.TaskRecord{
		ID: "CW-TARGET-FAIL-001", Title: "executor task", Status: "doing",
		Executor: "cli", AgentProfile: "torque-backend",
		Kind: "agent", OnDone: "review",
	}
	require.NoError(t, store.CreateTask(target))

	require.NoError(t, lm.HandleResult(target.ID, 1, &executor.ExecutionResult{
		Status: "done",
	}))

	// The transition must stand even though the enqueue failed.
	post, err := store.GetTask(target.ID)
	require.NoError(t, err)
	assert.Equal(t, "review", post.Status)

	// No end-agent task was created.
	internals, err := store.ListTasks(sqlstore.TaskFilter{
		Kind:     "internal",
		ParentID: target.ID,
	})
	require.NoError(t, err)
	assert.Empty(t, internals, "enqueue failed — no end-agent task should exist")

	// Observable signal 1: a failure comment on the target with the
	// canonical end-agent author prefix the orchestrator greps for.
	comments, err := store.ListComments(target.ID)
	require.NoError(t, err)
	require.Len(t, comments, 1, "exactly one enqueue-failure comment on target")
	assert.Equal(t, scheduler.EndAgentAuthor, comments[0].Author)
	assert.Contains(t, comments[0].Content, "failed to enqueue")
	assert.Contains(t, comments[0].Content, target.ID)

	// Observable signal 2: an end_agent_enqueue_failed run_event breadcrumb.
	events, err := store.ListRunEvents(sqlstore.RunEventFilter{
		TaskID: target.ID,
		Types:  []string{"end_agent_enqueue_failed"},
	})
	require.NoError(t, err)
	require.Len(t, events, 1, "end_agent_enqueue_failed run_event must be emitted")
}

// CW-20260519-0081: the regression guard for the intermittent bug. Many
// kind=agent tasks transitioning to `review` concurrently must EACH get
// exactly one end-agent, all with distinct IDs. The pre-fix code allocated
// the ID and inserted in two separate auto-commit statements, so a concurrent
// allocation could claim the same MAX(id)+1 and the losing INSERT failed a
// UNIQUE constraint — silently dropping that target's reviewer. Allocating +
// inserting inside one writer-locked transaction makes it deterministic.
func TestEndAgent_ConcurrentReviewsEachGetExactlyOneEndAgent(t *testing.T) {
	store := setupEndAgentStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	const n = 12
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("CW-CONC-%03d", i)
		ids[i] = id
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: id, Title: "executor task", Status: "doing",
			Executor: "cli", AgentProfile: "torque-backend",
			Kind: "agent", OnDone: "review",
		}))
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = lm.HandleResult(ids[i], int64(i+1), &executor.ExecutionResult{
				Status: "done",
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "HandleResult for %s", ids[i])
	}

	// Every target got exactly one end-agent; all end-agent IDs are unique.
	seen := map[string]bool{}
	for _, id := range ids {
		internals, err := store.ListTasks(sqlstore.TaskFilter{
			Kind:     "internal",
			ParentID: id,
		})
		require.NoError(t, err)
		require.Lenf(t, internals, 1, "target %s must get exactly one end-agent", id)
		endID := internals[0].ID
		assert.Falsef(t, seen[endID], "duplicate end-agent id %s — ID-allocation race regressed", endID)
		seen[endID] = true
		assert.Equal(t, 0, internals[0].MaxRetries, "AC5 zero-retry must survive the serialized create")
	}
	assert.Len(t, seen, n, "every target produced a distinctly-identified end-agent")

	// No target took the observable-failure path.
	for _, id := range ids {
		comments, err := store.ListComments(id)
		require.NoError(t, err)
		assert.Emptyf(t, comments, "no enqueue-failure comment expected for %s", id)
	}
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
		Executor: "cli", AgentProfile: "torque-backend",
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
