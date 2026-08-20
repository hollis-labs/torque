package sqlstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SprintRecord mirrors the sprints table row. ArchivedAt is NULL for active
// sprints; non-NULL means the sprint is archived (kept for audit, hidden
// from active filters). Archiving is independent of Status.
type SprintRecord struct {
	ID           string
	Name         string
	Goal         string
	Status       string
	ApprovalMode string
	CostBudget   sql.NullFloat64
	ProjectID    sql.NullString
	StartedAt    sql.NullTime
	EndedAt      sql.NullTime
	ArchivedAt   sql.NullTime
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SprintFilter holds optional filter criteria for ListSprints.
// IncludeArchived defaults to false: archived rows (archived_at IS NOT
// NULL) are excluded unless the caller explicitly asks for them.
type SprintFilter struct {
	Status          string
	ProjectID       string
	IncludeArchived bool
	Limit           int
	Offset          int
}

// SprintUpdate holds optional fields to update; nil pointer = no change.
type SprintUpdate struct {
	Name         *string
	Goal         *string
	ApprovalMode *string
	CostBudget   *float64
	ProjectID    *string
}

// CreateSprint inserts a new sprint with defaults applied.
func (s *Store) CreateSprint(sp *SprintRecord) error {
	if sp.Status == "" {
		sp.Status = "active"
	}
	if sp.ApprovalMode == "" {
		sp.ApprovalMode = "approve_each"
	}

	_, err := s.db.Exec(`INSERT INTO sprints (
		id, name, goal, status, approval_mode, cost_budget, project_id, started_at, ended_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sp.ID, sp.Name, sp.Goal, sp.Status, sp.ApprovalMode,
		sp.CostBudget, sp.ProjectID, sp.StartedAt, sp.EndedAt,
	)
	return err
}

// GetSprint fetches a single sprint by ID.
func (s *Store) GetSprint(id string) (*SprintRecord, error) {
	sp := &SprintRecord{}
	err := s.ReadDB().QueryRow(`SELECT
		id, name, goal, status, approval_mode, cost_budget, project_id,
		started_at, ended_at, archived_at, created_at, updated_at
	FROM sprints WHERE id = ?`, id).Scan(
		&sp.ID, &sp.Name, &sp.Goal, &sp.Status, &sp.ApprovalMode,
		&sp.CostBudget, &sp.ProjectID, &sp.StartedAt, &sp.EndedAt,
		&sp.ArchivedAt, &sp.CreatedAt, &sp.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("sprint %s not found", id)
	}
	return sp, err
}

// ListSprints returns sprints matching the filter, ordered by created_at DESC.
// Archived rows (archived_at IS NOT NULL) are excluded unless
// f.IncludeArchived is true.
func (s *Store) ListSprints(f SprintFilter) ([]SprintRecord, error) {
	query := `SELECT id, name, goal, status, approval_mode, cost_budget, project_id,
		started_at, ended_at, archived_at, created_at, updated_at FROM sprints`

	var conditions []string
	var args []interface{}

	if f.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, f.Status)
	}
	if f.ProjectID != "" {
		conditions = append(conditions, "project_id = ?")
		args = append(args, f.ProjectID)
	}
	if !f.IncludeArchived {
		conditions = append(conditions, "archived_at IS NULL")
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
		if f.Offset > 0 {
			query += fmt.Sprintf(" OFFSET %d", f.Offset)
		}
	}

	rows, err := s.ReadDB().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sprints []SprintRecord
	for rows.Next() {
		var sp SprintRecord
		if err := rows.Scan(
			&sp.ID, &sp.Name, &sp.Goal, &sp.Status, &sp.ApprovalMode,
			&sp.CostBudget, &sp.ProjectID, &sp.StartedAt, &sp.EndedAt,
			&sp.ArchivedAt, &sp.CreatedAt, &sp.UpdatedAt,
		); err != nil {
			return nil, err
		}
		sprints = append(sprints, sp)
	}
	return sprints, rows.Err()
}

// UpdateSprint applies non-nil pointer fields to the sprint row.
func (s *Store) UpdateSprint(id string, u SprintUpdate) error {
	var sets []string
	var args []interface{}

	if u.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *u.Name)
	}
	if u.Goal != nil {
		sets = append(sets, "goal = ?")
		args = append(args, *u.Goal)
	}
	if u.ApprovalMode != nil {
		sets = append(sets, "approval_mode = ?")
		args = append(args, *u.ApprovalMode)
	}
	if u.CostBudget != nil {
		sets = append(sets, "cost_budget = ?")
		args = append(args, *u.CostBudget)
	}
	if u.ProjectID != nil {
		sets = append(sets, "project_id = ?")
		if *u.ProjectID == "" {
			args = append(args, nil)
		} else {
			args = append(args, *u.ProjectID)
		}
	}

	if len(sets) == 0 {
		return nil
	}

	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)

	query := fmt.Sprintf("UPDATE sprints SET %s WHERE id = ?", strings.Join(sets, ", "))
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("sprint %s not found", id)
	}
	return nil
}

// TransitionSprint sets a new status on the sprint.
func (s *Store) TransitionSprint(id, newStatus string) error {
	result, err := s.db.Exec(
		"UPDATE sprints SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		newStatus, id,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("sprint %s not found", id)
	}
	return nil
}

// ArchiveSprint sets archived_at = CURRENT_TIMESTAMP. Idempotent: calling
// archive on an already-archived sprint refreshes the timestamp. Does not
// touch status — archiving is orthogonal to the sprint's workflow state.
func (s *Store) ArchiveSprint(id string) error {
	res, err := s.db.Exec(
		`UPDATE sprints SET archived_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("sprint %s not found", id)
	}
	return nil
}

// UnarchiveSprint clears archived_at, restoring the sprint to active
// (in the archive sense; status is untouched).
func (s *Store) UnarchiveSprint(id string) error {
	res, err := s.db.Exec(
		`UPDATE sprints SET archived_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("sprint %s not found", id)
	}
	return nil
}

// DeleteSprint removes a sprint and clears sprint_id on associated tasks.
func (s *Store) DeleteSprint(id string) error {
	// Clear sprint_id on any tasks referencing this sprint
	s.db.Exec("UPDATE tasks SET sprint_id = NULL WHERE sprint_id = ?", id)

	result, err := s.db.Exec("DELETE FROM sprints WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("sprint %s not found", id)
	}
	return nil
}

// NextSprintID generates the next sequential sprint ID for today.
func (s *Store) NextSprintID() (string, error) {
	date := time.Now().Format("20060102")
	prefix := "SP-" + date + "-"

	var maxSeq int
	err := s.ReadDB().QueryRow(
		"SELECT COALESCE(MAX(CAST(SUBSTR(id, ?) AS INTEGER)), 0) FROM sprints WHERE id LIKE ?",
		len(prefix)+1, prefix+"%",
	).Scan(&maxSeq)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%04d", prefix, maxSeq+1), nil
}

// SprintCostUsed calculates the total cost of all runs for tasks in a sprint.
func (s *Store) SprintCostUsed(sprintID string) (float64, error) {
	var total sql.NullFloat64
	err := s.ReadDB().QueryRow(`
		SELECT COALESCE(SUM(r.cost), 0)
		FROM runs r
		INNER JOIN tasks t ON r.task_id = t.id
		WHERE t.sprint_id = ?
	`, sprintID).Scan(&total)
	if err != nil {
		return 0, err
	}
	if total.Valid {
		return total.Float64, nil
	}
	return 0, nil
}
