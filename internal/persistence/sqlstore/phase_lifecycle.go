package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
)

// Phase lifecycle (CW-20261016-0134, T2). A phase starts when work in it
// starts, completes when all of its work is done, and is reopened only by an
// explicit call. The edges are written here, in the transaction of the change
// that causes them, and each edge appends a phase.started / phase.completed
// event after the task.state_changed it follows.
//
//   - A pending phase starts when a member moves to doing (or review, blocked,
//     paused, done). The last member going straight to done with no earlier
//     start writes both edges, started then completed, in order.
//   - A phase completes when it has members and every member is done. Review,
//     archived and the other states are not success.
//   - A blocked member keeps the phase started; there is no phase.blocked.
//   - A completed phase never reverts on its own. Reopen is explicit and bumps
//     the lifecycle generation.
//   - An empty phase has no member to move it; it needs an explicit start or
//     complete (not yet exposed).
//   - Only a member's status change drives an edge. A member added to or taken
//     out of a phase does not re-evaluate it.

// startsPhase is the set of member statuses that mean work in the phase has
// begun (the same set the 043 backfill used to derive a started phase).
var startsPhase = map[string]bool{"doing": true, "review": true, "blocked": true, "paused": true, "done": true}

// phasePayload is phase.lifecycle@1 (urn:hollis-labs:events:phase.lifecycle@1);
// unknown fields are allowed, so the lifecycle generation rides along.
type phasePayload struct {
	PlanID     string        `json:"plan_id"`
	PhaseID    string        `json:"phase_id"`
	Name       string        `json:"name"`
	Order      int           `json:"order"`
	Progress   phaseProgress `json:"progress"`
	Generation int           `json:"lifecycle_generation"`
}

type phaseProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// taskScope is the verifiable scope of a task's events: its project, epic and
// sprint, and its plan and phase from the plan_phases records. A plan task is
// its own plan. Missing is allowed; wrong is not.
func (w *WriteTx) taskScope(id string) (stateScope, error) {
	var project, epic, sprint, parent, phase sql.NullString
	var kind string
	if err := w.tx.QueryRow(
		`SELECT project_id, epic_id, sprint_id, parent_id, phase_id, kind FROM tasks WHERE id = ?`, id,
	).Scan(&project, &epic, &sprint, &parent, &phase, &kind); err != nil {
		if err == sql.ErrNoRows {
			return stateScope{}, fmt.Errorf("task %s not found", id)
		}
		return stateScope{}, err
	}
	scope := stateScope{ProjectID: project.String, EpicID: epic.String, SprintID: sprint.String, TaskID: id}
	switch {
	case kind == "plan":
		scope.PlanID = id
	case phase.Valid && parent.Valid:
		// Triggers guarantee phase_id names a live phase of parent_id.
		scope.PlanID, scope.PhaseID = parent.String, phase.String
	}
	return scope, nil
}

// reconcilePhase applies the lifecycle rules after a member's status changed
// to newStatus. cause is the member's task.state_changed event id.
func (w *WriteTx) reconcilePhase(member stateScope, newStatus string, trigger Trigger, cause string) error {
	if member.PhaseID == "" || member.PlanID == "" {
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		done, err := w.reconcilePhaseOnce(member, newStatus, trigger, cause)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("phase %s/%s: lost the revision race three times", member.PlanID, member.PhaseID)
}

// reconcilePhaseOnce reports false when the revision-guarded update lost to a
// concurrent writer, so the caller re-reads and decides again.
func (w *WriteTx) reconcilePhaseOnce(member stateScope, newStatus string, trigger Trigger, cause string) (bool, error) {
	ph, err := w.getPhase(member.PlanID, member.PhaseID)
	if err != nil {
		return false, err
	}
	if ph.DeletedAt.Valid || ph.State == PhaseCompleted {
		return true, nil
	}
	var done, total int
	if err := w.tx.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0)
		   FROM tasks WHERE parent_id = ? AND phase_id = ?`, member.PlanID, member.PhaseID,
	).Scan(&total, &done); err != nil {
		return false, err
	}

	start := ph.State == PhasePending && (startsPhase[newStatus] || (total > 0 && done == total))
	complete := (ph.State == PhaseStarted || start) && total > 0 && done == total
	if !start && !complete {
		return true, nil
	}

	now := phaseNow()
	state := ph.State
	if start {
		state = PhaseStarted
	}
	var completedAt sql.NullString
	if complete {
		state = PhaseCompleted
		completedAt = sql.NullString{String: now, Valid: true}
	}
	res, err := w.tx.Exec(
		`UPDATE plan_phases SET state = ?, started_at = COALESCE(started_at, ?), completed_at = ?,
		        revision = revision + 1, updated_at = ?
		  WHERE plan_id = ? AND phase_id = ? AND revision = ? AND deleted_at IS NULL`,
		state, now, completedAt, now, ph.PlanID, ph.PhaseID, ph.Revision)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, err
	} else if n == 0 {
		return false, nil
	}

	scope, err := w.taskScope(member.PlanID)
	if err != nil {
		return false, err
	}
	scope.PhaseID = ph.PhaseID
	emit := func(kind string) error {
		return w.appendPhaseEvent(kind, ph, phaseProgress{Done: done, Total: total}, scope, trigger, &cause)
	}
	if start {
		if err := emit(catalog.KindPhaseStarted); err != nil {
			return false, err
		}
	}
	if complete {
		if err := emit(catalog.KindPhaseCompleted); err != nil {
			return false, err
		}
	}
	return true, nil
}

// appendPhaseEvent writes one phase edge. The subject is the phase
// (msg://phase/torque/<plan>/<phase>); the scope carries the plan and phase.
// A start that precedes a completion in the same transaction is the cause of
// the completion.
func (w *WriteTx) appendPhaseEvent(kind string, ph PlanPhaseRecord, prog phaseProgress, scope stateScope, trigger Trigger, cause *string) error {
	payload, err := json.Marshal(phasePayload{
		PlanID: ph.PlanID, PhaseID: ph.PhaseID, Name: ph.Name, Order: ph.Order,
		Progress: prog, Generation: ph.Generation,
	})
	if err != nil {
		return fmt.Errorf("phase event payload: %w", err)
	}
	c := ""
	if cause != nil {
		c = *cause
	}
	id, err := w.appendEvent(eventSpec{
		Kind:       kind,
		Category:   "state",
		EntityType: "phase",
		EntityID:   ph.PlanID + "/" + ph.PhaseID,
		Payload:    payload,
		Trigger:    trigger,
		Causation:  c,
		Scope:      scope,
	})
	if err != nil {
		return err
	}
	if cause != nil {
		*cause = id
	}
	return nil
}

func (w *WriteTx) getPhase(planID, phaseID string) (PlanPhaseRecord, error) {
	r, err := scanPlanPhase(w.tx.QueryRow(`SELECT `+planPhaseCols+` FROM plan_phases WHERE plan_id = ? AND phase_id = ?`, planID, phaseID))
	if err == sql.ErrNoRows {
		return r, fmt.Errorf("%w: %s", ErrPhaseNotFound, phaseID)
	}
	return r, err
}

// ErrPhaseNotCompleted is returned by a reopen of a phase that is not completed.
var ErrPhaseNotCompleted = fmt.Errorf("phase is not completed")

// ReopenPlanPhase reopens a completed phase: it returns to started, its
// lifecycle generation goes up by one and completed_at clears. It appends a
// phase.started event carrying the new generation. Reopening happens before
// work is added to or reactivated in the phase, because a completed phase does
// not revert on its own.
func (s *Store) ReopenPlanPhase(ctx context.Context, planID, phaseID string) (PlanPhaseRecord, error) {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return PlanPhaseRecord{}, err
	}
	defer w.Rollback()
	ph, err := w.getPhase(planID, phaseID)
	if err != nil {
		return ph, err
	}
	if ph.DeletedAt.Valid {
		return ph, fmt.Errorf("%w: %s", ErrPhaseNotFound, phaseID)
	}
	if ph.State != PhaseCompleted {
		return ph, fmt.Errorf("%w: %s is %s", ErrPhaseNotCompleted, phaseID, ph.State)
	}
	now := phaseNow()
	res, err := w.tx.Exec(
		`UPDATE plan_phases SET state = ?, lifecycle_generation = lifecycle_generation + 1, started_at = ?,
		        completed_at = NULL, revision = revision + 1, updated_at = ?
		  WHERE plan_id = ? AND phase_id = ? AND revision = ?`,
		PhaseStarted, now, now, planID, phaseID, ph.Revision)
	if err != nil {
		return ph, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return ph, err
	} else if n == 0 {
		return ph, fmt.Errorf("phase %s/%s changed during reopen; retry", planID, phaseID)
	}
	ph.State, ph.Generation, ph.Revision = PhaseStarted, ph.Generation+1, ph.Revision+1
	ph.StartedAt, ph.CompletedAt = sql.NullString{String: now, Valid: true}, sql.NullString{}

	var done, total int
	if err := w.tx.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0)
		   FROM tasks WHERE parent_id = ? AND phase_id = ?`, planID, phaseID,
	).Scan(&total, &done); err != nil {
		return ph, err
	}
	scope, err := w.taskScope(planID)
	if err != nil {
		return ph, err
	}
	scope.PhaseID = phaseID
	if err := w.appendPhaseEvent(catalog.KindPhaseStarted, ph, phaseProgress{Done: done, Total: total}, scope, resolveTrigger(ctx, TransitionOpts{}), nil); err != nil {
		return ph, err
	}
	if err := w.Commit(); err != nil {
		return ph, err
	}
	return ph, nil
}
