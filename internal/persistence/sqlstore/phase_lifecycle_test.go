package sqlstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
)

// phasePlan makes a plan with one phase and n member tasks (todo) in it.
func phasePlan(t *testing.T, s *sqlstore.Store, n int) (plan string, members []string) {
	t.Helper()
	plan = "CW-PH-PLAN"
	require.NoError(t, s.CreateTask(&sqlstore.TaskRecord{ID: plan, Title: "plan", Kind: "plan", Manual: true, Priority: 2}))
	phases, err := s.AddPlanPhases(context.Background(), plan, []sqlstore.PlanPhaseInput{{Name: "Build"}})
	require.NoError(t, err)
	require.Equal(t, "ph-1", phases[0].PhaseID)
	for i := 0; i < n; i++ {
		id := "CW-PH-" + string(rune('A'+i))
		require.NoError(t, s.CreateTask(&sqlstore.TaskRecord{
			ID: id, Title: id, Priority: 2,
			ParentID: sql.NullString{String: plan, Valid: true},
			PhaseID:  sql.NullString{String: "ph-1", Valid: true},
		}))
		members = append(members, id)
	}
	return plan, members
}

func kinds(rows []outboxRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Kind)
	}
	return out
}

func phaseState(t *testing.T, s *sqlstore.Store, plan string) sqlstore.PlanPhaseRecord {
	t.Helper()
	ph, err := s.ListPlanPhases(plan, false)
	require.NoError(t, err)
	require.Len(t, ph, 1)
	return ph[0]
}

func TestPhaseLifecycle_StartsOnFirstWorkThenCompletesWhenAllDone(t *testing.T) {
	s := setupTestStore(t)
	plan, m := phasePlan(t, s, 2)

	require.NoError(t, s.TransitionTask(m[0], "doing"))
	rows := readOutbox(t, s)
	require.Equal(t, []string{"task.state_changed", "phase.started"}, kinds(rows))
	started := rows[1]
	require.Equal(t, rows[0].EventID, started.Env.Corr.CausationID, "the phase edge names the change that caused it")
	require.Equal(t, "msg://phase/torque/"+plan+"/ph-1", started.Env.Subject)
	require.Equal(t, plan, started.Env.Scope.PlanID)
	require.Equal(t, "ph-1", started.Env.Scope.PhaseID)
	require.Equal(t, plan, started.Env.Scope.TaskID)
	require.Equal(t, "phase.lifecycle@1", started.Env.Class.PayloadSchema[len(started.Env.Class.PayloadSchema)-len("phase.lifecycle@1"):])
	started.Env.Sequence = 1
	require.NoError(t, started.Env.Validate())

	var p struct {
		PlanID, PhaseID, Name string
		Order                 int
		Progress              struct{ Done, Total int }
		Generation            int `json:"lifecycle_generation"`
	}
	require.NoError(t, json.Unmarshal(started.Env.Payload, &p))
	require.Equal(t, "Build", p.Name)
	require.Equal(t, 2, p.Progress.Total)
	require.Equal(t, 0, p.Progress.Done)
	require.Equal(t, 1, p.Generation)

	// The member's own event now carries the plan and phase it belongs to.
	require.Equal(t, plan, rows[0].Env.Scope.PlanID)
	require.Equal(t, "ph-1", rows[0].Env.Scope.PhaseID)

	require.Equal(t, sqlstore.PhaseStarted, phaseState(t, s, plan).State)
	require.True(t, phaseState(t, s, plan).StartedAt.Valid)

	// More work in a started phase and one member done: no new edge.
	require.NoError(t, s.TransitionTask(m[1], "doing"))
	require.NoError(t, s.TransitionTask(m[0], "done"))
	require.Equal(t, []string{"task.state_changed", "phase.started", "task.state_changed", "task.state_changed"}, kinds(readOutbox(t, s)))

	require.NoError(t, s.TransitionTask(m[1], "done"))
	rows = readOutbox(t, s)
	require.Equal(t, []string{"task.state_changed", "phase.started", "task.state_changed", "task.state_changed", "task.state_changed", "phase.completed"}, kinds(rows))
	done := rows[5]
	require.Equal(t, rows[4].EventID, done.Env.Corr.CausationID)
	require.NoError(t, json.Unmarshal(done.Env.Payload, &p))
	require.Equal(t, 2, p.Progress.Done)
	require.Equal(t, 2, p.Progress.Total)
	ph := phaseState(t, s, plan)
	require.Equal(t, sqlstore.PhaseCompleted, ph.State)
	require.True(t, ph.CompletedAt.Valid)

	// Completed is not undone by a member moving back; reopen is explicit.
	require.NoError(t, s.TransitionTask(m[0], "doing"))
	require.Equal(t, sqlstore.PhaseCompleted, phaseState(t, s, plan).State)
	require.Len(t, readOutbox(t, s), 7, "only the member's own state change")
}

func TestPhaseLifecycle_LastMemberStraightToDoneWritesStartedThenCompleted(t *testing.T) {
	s := setupTestStore(t)
	_, m := phasePlan(t, s, 1)
	require.NoError(t, s.TransitionTask(m[0], "done"))
	rows := readOutbox(t, s)
	require.Equal(t, []string{"task.state_changed", "phase.started", "phase.completed"}, kinds(rows))
	require.Equal(t, rows[0].EventID, rows[1].Env.Corr.CausationID)
	require.Equal(t, rows[0].EventID, rows[2].Env.Corr.CausationID, "both edges share the cause")
	require.NotEqual(t, rows[1].EventID, rows[2].EventID, "each has its own id")
	require.Less(t, rows[1].Seq, rows[2].Seq)
}

func TestPhaseLifecycle_NoEdgeWithoutAChange(t *testing.T) {
	s := setupTestStore(t)
	plan, m := phasePlan(t, s, 2)

	// todo -> queued is not work starting; a no-op transition writes nothing.
	require.NoError(t, s.TransitionTask(m[0], "queued"))
	require.NoError(t, s.TransitionTask(m[0], "queued"))
	require.Equal(t, []string{"task.state_changed"}, kinds(readOutbox(t, s)))
	require.Equal(t, sqlstore.PhasePending, phaseState(t, s, plan).State)

	// A blocked member starts the phase and keeps it started.
	require.NoError(t, s.TransitionTask(m[1], "blocked"))
	require.Equal(t, sqlstore.PhaseStarted, phaseState(t, s, plan).State)
	require.Equal(t, "phase.started", kinds(readOutbox(t, s))[2])
}

func TestPhaseLifecycle_RollbackLeavesNoEdge(t *testing.T) {
	s := setupTestStore(t)
	plan, m := phasePlan(t, s, 1)
	w, err := s.BeginWriteTx(context.Background())
	require.NoError(t, err)
	require.NoError(t, w.TransitionTask(m[0], "done"))
	require.NoError(t, w.Rollback())
	require.Empty(t, readOutbox(t, s))
	require.Equal(t, sqlstore.PhasePending, phaseState(t, s, plan).State)
}

func TestPhaseLifecycle_ReopenBumpsGeneration(t *testing.T) {
	s := setupTestStore(t)
	plan, m := phasePlan(t, s, 1)
	ctx := context.Background()

	_, err := s.ReopenPlanPhase(ctx, plan, "ph-1")
	require.ErrorIs(t, err, sqlstore.ErrPhaseNotCompleted)

	require.NoError(t, s.TransitionTask(m[0], "done"))
	before := len(readOutbox(t, s))
	ph, err := s.ReopenPlanPhase(ctx, plan, "ph-1")
	require.NoError(t, err)
	require.Equal(t, 2, ph.Generation)
	require.Equal(t, sqlstore.PhaseStarted, ph.State)
	require.False(t, ph.CompletedAt.Valid)

	rows := readOutbox(t, s)
	require.Len(t, rows, before+1)
	last := rows[len(rows)-1]
	require.Equal(t, "phase.started", last.Kind)
	var p struct {
		Generation int `json:"lifecycle_generation"`
	}
	require.NoError(t, json.Unmarshal(last.Env.Payload, &p))
	require.Equal(t, 2, p.Generation)

	// Reactivated work completes the phase again, at the new generation.
	require.NoError(t, s.TransitionTask(m[0], "doing"))
	require.NoError(t, s.TransitionTask(m[0], "done"))
	rows = readOutbox(t, s)
	require.Equal(t, "phase.completed", rows[len(rows)-1].Kind)
	require.NoError(t, json.Unmarshal(rows[len(rows)-1].Env.Payload, &p))
	require.Equal(t, 2, p.Generation)
	require.Equal(t, 2, phaseState(t, s, plan).Generation)
}

func reopenedFlag(t *testing.T, r outboxRow) bool {
	t.Helper()
	var p struct {
		Reopened bool `json:"reopened"`
	}
	require.NoError(t, json.Unmarshal(r.Env.Payload, &p))
	return p.Reopened
}

func TestPhaseLifecycle_ReopenedMarksSecondAndLaterStarts(t *testing.T) {
	s := setupTestStore(t)
	plan, m := phasePlan(t, s, 1)
	require.NoError(t, s.TransitionTask(m[0], "done"))
	rows := readOutbox(t, s)
	require.False(t, reopenedFlag(t, rows[1]), "the first start is not a reopen")
	_, err := s.ReopenPlanPhase(context.Background(), plan, "ph-1")
	require.NoError(t, err)
	rows = readOutbox(t, s)
	require.True(t, reopenedFlag(t, rows[len(rows)-1]))
}

func addMember(t *testing.T, s *sqlstore.Store, plan, id, status string) {
	t.Helper()
	require.NoError(t, s.CreateTask(&sqlstore.TaskRecord{
		ID: id, Title: id, Priority: 2, Status: status,
		ParentID: sql.NullString{String: plan, Valid: true},
		PhaseID:  sql.NullString{String: "ph-1", Valid: true},
	}))
}

// Membership changes re-evaluate the phase, so it cannot stay started (or
// pending) after the change that settles it.
func TestPhaseLifecycle_MembershipChangesReevaluate(t *testing.T) {
	ctx := context.Background()

	t.Run("created already done starts and completes", func(t *testing.T) {
		s := setupTestStore(t)
		plan, _ := phasePlan(t, s, 0)
		addMember(t, s, plan, "CW-PH-X", "done")
		require.Equal(t, []string{"phase.started", "phase.completed"}, kinds(readOutbox(t, s)))
		require.Equal(t, sqlstore.PhaseCompleted, phaseState(t, s, plan).State)
	})

	t.Run("a done member added to a pending phase with open work starts it", func(t *testing.T) {
		s := setupTestStore(t)
		plan, _ := phasePlan(t, s, 1)
		addMember(t, s, plan, "CW-PH-X", "done")
		require.Equal(t, []string{"phase.started"}, kinds(readOutbox(t, s)))
		require.Equal(t, sqlstore.PhaseStarted, phaseState(t, s, plan).State)
	})

	t.Run("a todo member joining a started phase changes nothing", func(t *testing.T) {
		s := setupTestStore(t)
		plan, m := phasePlan(t, s, 1)
		require.NoError(t, s.TransitionTask(m[0], "doing"))
		before := len(readOutbox(t, s))
		addMember(t, s, plan, "CW-PH-X", "todo")
		require.Len(t, readOutbox(t, s), before)
		require.Equal(t, sqlstore.PhaseStarted, phaseState(t, s, plan).State)
	})

	t.Run("taking the last open member out completes it", func(t *testing.T) {
		s := setupTestStore(t)
		plan, m := phasePlan(t, s, 2)
		require.NoError(t, s.TransitionTask(m[0], "done"))
		require.Equal(t, sqlstore.PhaseStarted, phaseState(t, s, plan).State)
		out := sql.NullString{}
		require.NoError(t, s.UpdateTask(m[1], sqlstore.TaskUpdate{PhaseID: &out}))
		require.Equal(t, sqlstore.PhaseCompleted, phaseState(t, s, plan).State)
		rows := readOutbox(t, s)
		require.Equal(t, "phase.completed", rows[len(rows)-1].Kind)
		require.Empty(t, rows[len(rows)-1].Env.Corr.CausationID, "no task event caused it")
	})

	t.Run("moving the last open member to another plan completes it", func(t *testing.T) {
		s := setupTestStore(t)
		plan, m := phasePlan(t, s, 2)
		require.NoError(t, s.TransitionTask(m[0], "done"))
		require.NoError(t, s.CreateTask(&sqlstore.TaskRecord{ID: "CW-OTHER", Title: "o", Kind: "plan", Manual: true, Priority: 2}))
		_, err := s.AddPlanPhases(ctx, "CW-OTHER", []sqlstore.PlanPhaseInput{{Name: "X"}})
		require.NoError(t, err)
		to := sql.NullString{String: "CW-OTHER", Valid: true}
		ph := sql.NullString{String: "ph-1", Valid: true}
		require.NoError(t, s.UpdateTask(m[1], sqlstore.TaskUpdate{ParentID: &to, PhaseID: &ph}))
		require.Equal(t, sqlstore.PhaseCompleted, phaseState(t, s, plan).State)
	})

	t.Run("deleting the last open member completes it", func(t *testing.T) {
		s := setupTestStore(t)
		plan, m := phasePlan(t, s, 2)
		require.NoError(t, s.TransitionTask(m[0], "done"))
		require.NoError(t, s.DeleteTask(m[1]))
		require.Equal(t, sqlstore.PhaseCompleted, phaseState(t, s, plan).State)
	})

	t.Run("deleting every member leaves the phase as it was", func(t *testing.T) {
		s := setupTestStore(t)
		plan, m := phasePlan(t, s, 1)
		require.NoError(t, s.TransitionTask(m[0], "doing"))
		require.NoError(t, s.DeleteTask(m[0]))
		require.Equal(t, sqlstore.PhaseStarted, phaseState(t, s, plan).State, "an empty phase needs an explicit complete")
	})
}

func TestTaskScope_PlanTaskIsItsOwnPlan(t *testing.T) {
	s := setupTestStore(t)
	plan, _ := phasePlan(t, s, 0)
	require.NoError(t, s.TransitionTask(plan, "doing"))
	rows := readOutbox(t, s)
	require.Equal(t, plan, rows[0].Env.Scope.PlanID)
	require.Empty(t, rows[0].Env.Scope.PhaseID)
}

// Two transactions finishing the last two members at once must produce exactly
// one phase.completed, whichever commits second.
func TestPhaseLifecycle_ConcurrentLastMembersCompleteExactlyOnce(t *testing.T) {
	for round := 0; round < 10; round++ {
		s := setupTestStore(t)
		plan, m := phasePlan(t, s, 2)
		require.NoError(t, s.TransitionTask(m[0], "doing"))
		require.NoError(t, s.TransitionTask(m[1], "doing"))

		var wg sync.WaitGroup
		for _, id := range m {
			wg.Add(1)
			go func() {
				defer wg.Done()
				require.NoError(t, s.TransitionTask(id, "done"))
			}()
		}
		wg.Wait()

		completed := 0
		for _, r := range readOutbox(t, s) {
			if r.Kind == "phase.completed" {
				completed++
			}
		}
		require.Equal(t, 1, completed, "round %d", round)
		require.Equal(t, sqlstore.PhaseCompleted, phaseState(t, s, plan).State)
	}
}

// A forced re-assertion writes a task event but is not a change in the members.
func TestPhaseLifecycle_ForcedSelfTransitionAddsNoEdge(t *testing.T) {
	s := setupTestStore(t)
	plan, m := phasePlan(t, s, 1)
	require.NoError(t, s.TransitionTask(m[0], "done"))
	before := kinds(readOutbox(t, s))
	require.NoError(t, s.TransitionTaskOpts(context.Background(), m[0], "done", sqlstore.TransitionOpts{Forced: true}))
	after := kinds(readOutbox(t, s))
	require.Equal(t, append(before, "task.state_changed"), after, "only the re-assertion itself")
	require.Equal(t, sqlstore.PhaseCompleted, phaseState(t, s, plan).State)
}

// The phase edge is attributed to whoever made the member change.
func TestPhaseLifecycle_EdgesInheritTheTrigger(t *testing.T) {
	s := setupTestStore(t)
	_, m := phasePlan(t, s, 1)
	ctx := sqlstore.ContextWithTrigger(context.Background(), sqlstore.Trigger{Kind: sqlstore.TriggerAgent, Ref: "mcp:torque_task_transition"})
	require.NoError(t, s.TransitionTaskOpts(ctx, m[0], "done", sqlstore.TransitionOpts{}))
	rows := readOutbox(t, s)
	require.Equal(t, []string{"task.state_changed", "phase.started", "phase.completed"}, kinds(rows))
	for _, r := range rows {
		require.Equal(t, "agent", r.Trigger, r.Kind)
	}

	// Explicit scheduler trigger on a fresh phase.
	s2 := setupTestStore(t)
	_, m2 := phasePlan(t, s2, 1)
	require.NoError(t, s2.TransitionTaskOpts(context.Background(), m2[0], "doing", sqlstore.TransitionOpts{
		Trigger: sqlstore.Trigger{Kind: sqlstore.TriggerScheduler, Ref: "scheduler"}}))
	rows = readOutbox(t, s2)
	require.Equal(t, "phase.started", rows[1].Kind)
	require.Equal(t, "scheduler", rows[1].Trigger)
}

// Run events for a phase member carry the plan and phase, like its task events.
func TestRunEventsCarryPlanAndPhaseScope(t *testing.T) {
	s := setupTestStore(t)
	plan, m := phasePlan(t, s, 1)
	_, err := s.CreateRun(&sqlstore.RunRecord{TaskID: m[0], Executor: "cli"})
	require.NoError(t, err)
	var found bool
	for _, r := range readOutbox(t, s) {
		if r.Kind == "run.state_changed" {
			found = true
			require.Equal(t, plan, r.Env.Scope.PlanID)
			require.Equal(t, "ph-1", r.Env.Scope.PhaseID)
		}
	}
	require.True(t, found, "a run.state_changed event")
}
