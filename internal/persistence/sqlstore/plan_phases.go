package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Plan phases are records (migration 043, CW-20261016-0134): a row per
// (plan, phase) with a state, lifecycle times and a revision, instead of an
// element of the plan task's metadata. This file owns their writes; tasks join
// a phase through tasks.phase_id.

// Phase states.
const (
	PhasePending   = "pending"
	PhaseStarted   = "started"
	PhaseCompleted = "completed"
)

var (
	// ErrInvalidPhase is returned when a task write names a phase that is not
	// a live phase of the task's parent plan (enforced by database triggers).
	ErrInvalidPhase = errors.New("invalid phase")
	// ErrPhaseNotFound means the plan has no live phase with that id.
	ErrPhaseNotFound = errors.New("phase not found")
	// ErrPhaseInUse means tasks still belong to the phase.
	ErrPhaseInUse = errors.New("phase has member tasks")
)

// PhaseInUseError is the ErrPhaseInUse failure with the member count.
type PhaseInUseError struct {
	PhaseID string
	Members int
}

func (e *PhaseInUseError) Error() string {
	return fmt.Sprintf("%v: %d task(s) still belong to %s", ErrPhaseInUse, e.Members, e.PhaseID)
}

func (e *PhaseInUseError) Is(target error) bool { return target == ErrPhaseInUse }

// PlanPhaseRecord is one plan_phases row.
type PlanPhaseRecord struct {
	PlanID     string
	PhaseID    string
	Name       string
	Order      int
	Acceptance string
	State      string
	// Generation is the lifecycle generation: 1 until an explicit reopen of a
	// completed phase bumps it.
	Generation  int
	StartedAt   sql.NullString
	CompletedAt sql.NullString
	// Revision counts writes to the row, for optimistic concurrency.
	Revision  int
	DeletedAt sql.NullString
	CreatedAt string
	UpdatedAt string
}

// PlanPhaseInput is a phase to add; empty names are skipped by the caller.
type PlanPhaseInput struct {
	Name       string
	Acceptance string
}

const planPhaseCols = `plan_id, phase_id, name, display_order, acceptance, state, lifecycle_generation,
	started_at, completed_at, revision, deleted_at, created_at, updated_at`

func scanPlanPhase(row interface{ Scan(...any) error }) (PlanPhaseRecord, error) {
	var r PlanPhaseRecord
	err := row.Scan(&r.PlanID, &r.PhaseID, &r.Name, &r.Order, &r.Acceptance, &r.State, &r.Generation,
		&r.StartedAt, &r.CompletedAt, &r.Revision, &r.DeletedAt, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// phaseWriteErr maps the trigger's failure on a task write to ErrInvalidPhase.
// The trigger text is the only signal both SQLite and Postgres give.
func phaseWriteErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "invalid phase:") {
		return fmt.Errorf("%w: phase_id must name a live phase of the parent plan", ErrInvalidPhase)
	}
	return err
}

// ListPlanPhases returns a plan's phases in display order. Tombstoned phases
// are included only when includeDeleted is set.
func (s *Store) ListPlanPhases(planID string, includeDeleted bool) ([]PlanPhaseRecord, error) {
	q := `SELECT ` + planPhaseCols + ` FROM plan_phases WHERE plan_id = ?`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	q += ` ORDER BY display_order, phase_id`
	rows, err := s.ReadDB().Query(q, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanPhaseRecord
	for rows.Next() {
		r, err := scanPlanPhase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func phaseNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// AddPlanPhases appends phases to a plan in one transaction and returns the
// new records. Ids are "ph-N", allocated above every suffix the plan has ever
// issued, tombstones included, so an id is never handed out twice; display
// order follows the highest live order.
func (s *Store) AddPlanPhases(ctx context.Context, planID string, in []PlanPhaseInput) ([]PlanPhaseRecord, error) {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Rollback()
	out, err := w.AddPlanPhases(planID, in)
	if err != nil {
		return nil, err
	}
	if err := w.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// AddPlanPhases is Store.AddPlanPhases inside the caller's transaction.
func (w *WriteTx) AddPlanPhases(planID string, in []PlanPhaseInput) ([]PlanPhaseRecord, error) {
	var maxSuffix, maxOrder int
	rows, err := w.tx.Query(`SELECT phase_id, display_order, deleted_at FROM plan_phases WHERE plan_id = ?`, planID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var order int
		var deleted sql.NullString
		if err := rows.Scan(&id, &order, &deleted); err != nil {
			rows.Close()
			return nil, err
		}
		if n, ok := phaseSuffix(id); ok && n > maxSuffix {
			maxSuffix = n
		}
		if !deleted.Valid && order > maxOrder {
			maxOrder = order
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	now := phaseNow()
	out := make([]PlanPhaseRecord, 0, len(in))
	for _, p := range in {
		maxSuffix++
		maxOrder++
		r := PlanPhaseRecord{
			PlanID: planID, PhaseID: "ph-" + strconv.Itoa(maxSuffix), Name: p.Name, Order: maxOrder,
			Acceptance: p.Acceptance, State: PhasePending, Generation: 1, Revision: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		if _, err := w.tx.Exec(`INSERT INTO plan_phases (plan_id, phase_id, name, display_order, acceptance,
			state, lifecycle_generation, revision, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			r.PlanID, r.PhaseID, r.Name, r.Order, r.Acceptance, r.State, r.Generation, r.Revision, now, now); err != nil {
			return nil, fmt.Errorf("add phase %s: %w", r.PhaseID, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// phaseSuffix parses the N of a "ph-N" id.
func phaseSuffix(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, "ph-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n > 0
}

// RemovePlanPhase tombstones a live phase. It fails with ErrPhaseInUse while
// any task still belongs to it and with ErrPhaseNotFound when the plan has no
// live phase of that id. The row stays so the id is never reused.
func (s *Store) RemovePlanPhase(ctx context.Context, planID, phaseID string) error {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()
	if err := w.RemovePlanPhase(planID, phaseID); err != nil {
		return err
	}
	return w.Commit()
}

// RemovePlanPhase is Store.RemovePlanPhase inside the caller's transaction.
func (w *WriteTx) RemovePlanPhase(planID, phaseID string) error {
	var members int
	if err := w.tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE parent_id = ? AND phase_id = ?`, planID, phaseID).Scan(&members); err != nil {
		return err
	}
	if members > 0 {
		return &PhaseInUseError{PhaseID: phaseID, Members: members}
	}
	now := phaseNow()
	res, err := w.tx.Exec(`UPDATE plan_phases SET deleted_at = ?, updated_at = ?, revision = revision + 1
		WHERE plan_id = ? AND phase_id = ? AND deleted_at IS NULL`, now, now, planID, phaseID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrPhaseNotFound, phaseID)
	}
	return nil
}
