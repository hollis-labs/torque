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
//   - A pending phase starts once any member is doing, review, blocked, paused
//     or done. If that same change also leaves every member done (the last
//     member going straight to done), both edges are written, started then
//     completed, in order, sharing the same cause.
//   - A phase completes when it has members and every member is done. Review,
//     archived and the other states are not success.
//   - A blocked member keeps the phase started; there is no phase.blocked.
//   - A completed phase never reverts on its own. Reopen is explicit and bumps
//     the lifecycle generation.
//   - An empty phase has no member to move it; it needs an explicit start or
//     complete (not yet exposed).
//   - Anything that changes the members re-evaluates the phase in the same
//     transaction: a status change, a member added, moved out, deleted, or
//     created already done. A phase never waits on a change nobody notices.

// phasePayload is phase.lifecycle@1 (urn:hollis-labs:events:phase.lifecycle@1);
// unknown fields are allowed, so the lifecycle generation rides along.
type phasePayload struct {
	PlanID     string        `json:"plan_id"`
	PhaseID    string        `json:"phase_id"`
	Name       string        `json:"name"`
	Order      int           `json:"order"`
	Progress   phaseProgress `json:"progress"`
	Generation int           `json:"lifecycle_generation"`
	// Reopened marks a start after the first (an explicit reopen).
	Reopened bool `json:"reopened,omitempty"`
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

// phaseRef names a phase. The zero value (no phase) is ignored.
type phaseRef struct{ PlanID, PhaseID string }

// membership reads which phase a task currently belongs to.
func (w *WriteTx) membership(taskID string) (phaseRef, error) {
	var parent, phase sql.NullString
	if err := w.tx.QueryRow(`SELECT parent_id, phase_id FROM tasks WHERE id = ?`, taskID).Scan(&parent, &phase); err != nil {
		return phaseRef{}, err
	}
	if !parent.Valid || !phase.Valid {
		return phaseRef{}, nil
	}
	return phaseRef{PlanID: parent.String, PhaseID: phase.String}, nil
}

// reconcileMembership re-evaluates a phase after a task joined it, left it or
// was created in it. No task event caused the edge, so it has no causation.
func (w *WriteTx) reconcileMembership(ref phaseRef) error {
	return w.reconcilePhase(ref, resolveTrigger(w.ctx, TransitionOpts{}), "")
}

// reconcilePhase derives a phase's state from its members and writes the edge,
// if any. It is the one place the rules apply, whatever changed the members: a
// status change (cause is the member's task.state_changed id), or a member
// added, removed, deleted or created.
//
// The phase row is read FOR UPDATE on Postgres (SQLite's BEGIN IMMEDIATE
// already serializes writers) before the members are counted, so two
// transactions finishing the last two members cannot both see "one left".
func (w *WriteTx) reconcilePhase(ref phaseRef, trigger Trigger, cause string) error {
	if ref.PlanID == "" || ref.PhaseID == "" {
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		done, err := w.reconcilePhaseOnce(ref, trigger, cause)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("phase %s/%s: lost the revision race three times", ref.PlanID, ref.PhaseID)
}

// reconcilePhaseOnce reports false when the revision-guarded update lost to a
// concurrent writer, so the caller re-reads and decides again.
func (w *WriteTx) reconcilePhaseOnce(ref phaseRef, trigger Trigger, cause string) (bool, error) {
	ph, err := w.getPhaseLocked(ref.PlanID, ref.PhaseID)
	if err != nil {
		return false, err
	}
	if ph.DeletedAt.Valid || ph.State == PhaseCompleted {
		return true, nil
	}
	var total, done, begun int
	if err := w.tx.QueryRow(
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status IN ('doing','review','blocked','paused','done') THEN 1 ELSE 0 END), 0)
		   FROM tasks WHERE parent_id = ? AND phase_id = ?`, ref.PlanID, ref.PhaseID,
	).Scan(&total, &done, &begun); err != nil {
		return false, err
	}

	start := ph.State == PhasePending && begun > 0
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

	scope, err := w.taskScope(ref.PlanID)
	if err != nil {
		return false, err
	}
	scope.PhaseID = ph.PhaseID
	prog := phaseProgress{Done: done, Total: total}
	// Started and completed in one pass share the same cause; each has its own id.
	if start {
		if err := w.appendPhaseEvent(catalog.KindPhaseStarted, ph, prog, scope, trigger, cause); err != nil {
			return false, err
		}
	}
	if complete {
		if err := w.appendPhaseEvent(catalog.KindPhaseCompleted, ph, prog, scope, trigger, cause); err != nil {
			return false, err
		}
	}
	return true, nil
}

// appendPhaseEvent writes one phase edge. The subject is the phase
// (msg://phase/torque/<plan>/<phase>: phase ids are per plan, and the subject
// never changes for a phase); the scope carries the plan and phase. A start
// after the first (generation > 1) is marked reopened.
func (w *WriteTx) appendPhaseEvent(kind string, ph PlanPhaseRecord, prog phaseProgress, scope stateScope, trigger Trigger, cause string) error {
	payload, err := json.Marshal(phasePayload{
		PlanID: ph.PlanID, PhaseID: ph.PhaseID, Name: ph.Name, Order: ph.Order,
		Progress: prog, Generation: ph.Generation,
		Reopened: kind == catalog.KindPhaseStarted && ph.Generation > 1,
	})
	if err != nil {
		return fmt.Errorf("phase event payload: %w", err)
	}
	_, err = w.appendEvent(eventSpec{
		Kind:       kind,
		Category:   "state",
		EntityType: "phase",
		EntityID:   ph.PlanID + "/" + ph.PhaseID,
		Payload:    payload,
		Trigger:    trigger,
		Causation:  cause,
		Scope:      scope,
	})
	return err
}

func (w *WriteTx) getPhase(planID, phaseID string) (PlanPhaseRecord, error) {
	return w.readPhase(planID, phaseID, "")
}

// getPhaseLocked is getPhase taking a row lock on Postgres.
func (w *WriteTx) getPhaseLocked(planID, phaseID string) (PlanPhaseRecord, error) {
	lock := ""
	if _, pg := w.store.dialect.(postgresDialect); pg {
		lock = " FOR UPDATE"
	}
	return w.readPhase(planID, phaseID, lock)
}

func (w *WriteTx) readPhase(planID, phaseID, lock string) (PlanPhaseRecord, error) {
	r, err := scanPlanPhase(w.tx.QueryRow(`SELECT `+planPhaseCols+` FROM plan_phases WHERE plan_id = ? AND phase_id = ?`+lock, planID, phaseID))
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
	if err := w.appendPhaseEvent(catalog.KindPhaseStarted, ph, phaseProgress{Done: done, Total: total}, scope, resolveTrigger(ctx, TransitionOpts{}), ""); err != nil {
		return ph, err
	}
	if err := w.Commit(); err != nil {
		return ph, err
	}
	return ph, nil
}
