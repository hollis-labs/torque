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

	// Budget-range filter (ENT-SPRINT). CostBudgetMin/Max compare against
	// the sprints.cost_budget column directly; a nil bound is not applied.
	// A NULL cost_budget row never matches either bound (SQL NULL
	// comparison), which is the desired behavior — a sprint with no budget
	// set shouldn't show up in a budget-range query.
	CostBudgetMin *float64
	CostBudgetMax *float64
	// OverBudget, when true, restricts results to sprints with a cost_budget
	// set whose total run cost (see SprintCostUsed) exceeds it — the "over
	// budget" filter noted as missing in the audit (mcp-service-layer-audit
	// §Sprint). A sprint with no budget can never be "over budget", so this
	// implicitly excludes NULL-budget rows too.
	OverBudget bool

	Limit  int
	Offset int

	// Sort + cursor pagination (PRIM-002 / PRIM-001, DEC-001's binding
	// spec) — mirrors TaskFilter's SortBy/SortDir/AfterSortValue/AfterID
	// contract (see tasks.go for the full rationale). SortBy/SortDir are
	// expected to already be validated by the caller (mcpadapter validates
	// against an explicit allow-list via
	// internal/service/pagination.ValidateSortBy/ValidateSortDir) before
	// this filter is built; ListSprints does not itself reject an
	// unrecognized SortBy, it just treats it the same as "" (see
	// sprintSortColumn) and falls back to the original hardcoded
	// `updated_at DESC` order — the order FIX-004 confirmed
	// torque_sprint_list's docstring should describe.
	SortBy         string
	SortDir        string
	AfterSortValue string
	AfterID        string
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

// sprintSortColumn maps a sort_by value to its backing SQL column for
// ListSprints (PRIM-002). Mirrors taskSortColumn's contract: the MCP layer
// validates sort_by against an explicit allow-list
// (internal/service/pagination.ValidateSortBy) before ever reaching here, so
// this is a closed, trusted set — but ListSprints stays defensive: an empty
// or unrecognized sort_by both return "", which ListSprints treats
// identically as "no sort_by supplied" and falls back to the original
// hardcoded `updated_at DESC` order. That fallback is what keeps callers
// that haven't adopted the sort/cursor primitive (HTTP /api/v1/sprints)
// working unchanged.
func sprintSortColumn(sortBy string) string {
	switch sortBy {
	case "name", "status", "updated_at", "created_at":
		return sortBy
	default:
		return ""
	}
}

// sprintCursorArg converts a cursor's string-encoded sort value (DEC-001's
// `sv` field) into the correctly-typed SQL bind argument for sortBy's
// column. name/status are plain strings; updated_at/created_at are
// validated against SQLiteDatetimeLayout (mirroring taskCursorArg's
// rationale in tasks.go — sprints.updated_at/created_at are always written
// via SQL-side CURRENT_TIMESTAMP, so unlike tasks there's no Go-side
// time.Time formatting mismatch to route around here; validating the shape
// is still worthwhile to reject a malformed/tampered cursor token cleanly).
func sprintCursorArg(sortBy, sv string) (any, error) {
	switch sortBy {
	case "name", "status":
		return sv, nil
	case "updated_at", "created_at":
		if _, err := time.Parse(SQLiteDatetimeLayout, sv); err != nil {
			return nil, fmt.Errorf("%w: sort value for %s must match %q: %v", ErrInvalidCursor, sortBy, SQLiteDatetimeLayout, err)
		}
		return sv, nil
	default:
		return nil, fmt.Errorf("%w: unsupported sort_by %q", ErrInvalidCursor, sortBy)
	}
}

// ListSprints returns sprints matching the filter. Default order (f.SortBy
// == "") is updated_at DESC — the order FIX-004 confirmed
// torque_sprint_list's docstring should describe. When f.SortBy is set
// (PRIM-002), order becomes `<sort column> <SortDir>, id ASC` and, if
// f.AfterID is also set, results are additionally filtered to rows after
// the cursor's (sort value, id) position (PRIM-001/DEC-001 keyset
// pagination). Archived rows (archived_at IS NOT NULL) are excluded unless
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
	if f.CostBudgetMin != nil {
		conditions = append(conditions, "cost_budget >= ?")
		args = append(args, *f.CostBudgetMin)
	}
	if f.CostBudgetMax != nil {
		conditions = append(conditions, "cost_budget <= ?")
		args = append(args, *f.CostBudgetMax)
	}
	if f.OverBudget {
		conditions = append(conditions, `cost_budget IS NOT NULL AND cost_budget < (
			SELECT COALESCE(SUM(r.cost), 0) FROM runs r
			INNER JOIN tasks t ON r.task_id = t.id
			WHERE t.sprint_id = sprints.id
		)`)
	}

	// PRIM-002 sort column + PRIM-001 cursor predicate. sortCol == "" means
	// "no sort_by supplied" (or an unrecognized one slipping past the MCP
	// layer's validation, defensively treated the same) — preserves the
	// original hardcoded default order below rather than the cursor path.
	sortCol := sprintSortColumn(f.SortBy)
	desc := strings.EqualFold(f.SortDir, "desc")
	if sortCol != "" && f.AfterID != "" {
		arg, err := sprintCursorArg(f.SortBy, f.AfterSortValue)
		if err != nil {
			return nil, err
		}
		cmp := ">"
		if desc {
			cmp = "<"
		}
		// Tuple comparison (sortCol, id) > (arg, AfterID), or the two-clause
		// equivalent below — tiebreak on id ascending regardless of
		// SortDir, per DEC-001.
		conditions = append(conditions, fmt.Sprintf("(%s %s ? OR (%s = ? AND id > ?))", sortCol, cmp, sortCol))
		args = append(args, arg, arg, f.AfterID)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	if sortCol != "" {
		dir := "ASC"
		if desc {
			dir = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY %s %s, id ASC", sortCol, dir)
	} else {
		query += " ORDER BY updated_at DESC"
	}

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
// The reference-nulling UPDATE and the sprint's own DELETE run inside a
// single transaction so a failed UPDATE (lock contention, disk full, etc.)
// can never leave the sprint deleted while tasks still point at it.
func (s *Store) DeleteSprint(id string) error {
	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear sprint_id on any tasks referencing this sprint
	if _, err := tx.Exec("UPDATE tasks SET sprint_id = NULL WHERE sprint_id = ?", id); err != nil {
		return err
	}

	result, err := tx.Exec("DELETE FROM sprints WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("sprint %s not found", id)
	}
	return tx.Commit()
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
