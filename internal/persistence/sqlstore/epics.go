package sqlstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// EpicRecord mirrors the epics table row. ArchivedAt is NULL for active
// epics; non-NULL means the epic is archived (kept for audit, hidden from
// active filters). Archiving is independent of Status.
type EpicRecord struct {
	ID          string
	Name        string
	Description string
	Status      string
	Priority    sql.NullInt64
	ProjectID   sql.NullString
	ArchivedAt  sql.NullTime
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// EpicFilter holds optional filter criteria for ListEpics.
// IncludeArchived defaults to false: archived rows (archived_at IS NOT
// NULL) are excluded unless the caller explicitly asks for them.
type EpicFilter struct {
	Status          string
	ProjectID       string
	Search          string // case-insensitive substring match on id, name, description
	IncludeArchived bool
	Limit           int
	Offset          int

	// Sort + cursor pagination (PRIM-002 / PRIM-001, DEC-001's binding
	// spec), mirroring TaskFilter's SortBy/SortDir/AfterSortValue/AfterID
	// (internal/persistence/sqlstore/tasks.go). SortBy/SortDir are expected
	// to already be validated by the caller (mcpadapter validates against
	// an explicit allow-list via internal/service/pagination.ValidateSortBy
	// before this filter is built) — ListEpics does not itself reject an
	// unrecognized SortBy, it just treats it the same as "" (see
	// epicSortColumn). Leaving SortBy empty preserves the original
	// hardcoded `updated_at DESC` order for callers that haven't adopted
	// the primitive (HTTP /api/v1/epics).
	//
	// AfterSortValue/AfterID decode DEC-001's opaque cursor token: the
	// string-encoded sort-column value and id of the last row the caller
	// already saw. Both empty means "first page".
	SortBy         string
	SortDir        string
	AfterSortValue string
	AfterID        string
}

// EpicUpdate holds optional fields to update; nil pointer = no change.
type EpicUpdate struct {
	Name        *string
	Description *string
	Status      *string
	Priority    *int64
	ProjectID   *string
}

// CreateEpic inserts a new epic with defaults applied.
func (s *Store) CreateEpic(e *EpicRecord) error {
	if e.Status == "" {
		e.Status = "active"
	}

	_, err := s.db.Exec(`INSERT INTO epics (id, name, description, status, priority, project_id) VALUES (?, ?, ?, ?, ?, ?)`,
		e.ID, e.Name, e.Description, e.Status, e.Priority, e.ProjectID,
	)
	return err
}

// GetEpic fetches a single epic by ID.
func (s *Store) GetEpic(id string) (*EpicRecord, error) {
	e := &EpicRecord{}
	err := s.ReadDB().QueryRow(`SELECT id, name, description, status, priority, project_id, archived_at, created_at, updated_at FROM epics WHERE id = ?`, id).Scan(
		&e.ID, &e.Name, &e.Description, &e.Status, &e.Priority, &e.ProjectID, &e.ArchivedAt, &e.CreatedAt, &e.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("epic %s not found", id)
	}
	return e, err
}

// epicSortColumn maps a validated sort_by value to its epics column name.
// Returns "" for an empty or unrecognized sortBy — treated as "no sort_by
// supplied", which preserves ListEpics' original hardcoded default order
// (see the ORDER BY branch below). Allow-list matches PRIM-002's guidance
// for Epic/Sprint/Project (name, status, updated_at, created_at) —
// intentionally excludes priority, which is not on that documented list.
func epicSortColumn(sortBy string) string {
	switch sortBy {
	case "name", "status", "updated_at", "created_at":
		return sortBy
	default:
		return ""
	}
}

// epicCursorArg validates the cursor value before the query builder binds it.
// Timestamp values are normalized by timestampCursorArg for the active dialect.
func epicCursorArg(sortBy, sv string) (any, error) {
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

// ListEpics returns epics matching the filter. Default order (f.SortBy ==
// "") is updated_at DESC — the order torque_epic_list's docstring
// describes. When f.SortBy is set (PRIM-002), order becomes `<sort column>
// <SortDir>, id ASC` and, if f.AfterID is also set, results are
// additionally filtered to rows after the cursor's (sort value, id)
// position (PRIM-001/DEC-001 keyset pagination). Archived rows
// (archived_at IS NOT NULL) are excluded unless f.IncludeArchived is true.
func (s *Store) ListEpics(f EpicFilter) ([]EpicRecord, error) {
	query := `SELECT id, name, description, status, priority, project_id, archived_at, created_at, updated_at FROM epics`

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
	if f.Search != "" {
		// SQLite's LIKE is case-insensitive for ASCII by default.
		pattern := "%" + f.Search + "%"
		conditions = append(conditions, "(id LIKE ? OR name LIKE ? OR description LIKE ?)")
		args = append(args, pattern, pattern, pattern)
	}

	// PRIM-002 sort column + PRIM-001 cursor predicate.
	sortCol := epicSortColumn(f.SortBy)
	// Cursor predicates and ordering must compare the same precise key.
	sortKey := s.timestampSortKey(sortCol)
	desc := strings.EqualFold(f.SortDir, "desc")
	if sortCol != "" && f.AfterID != "" {
		arg, err := epicCursorArg(f.SortBy, f.AfterSortValue)
		if err != nil {
			return nil, err
		}
		arg, err = s.timestampCursorArg(sortCol, arg)
		if err != nil {
			return nil, err
		}
		cmp := ">"
		if desc {
			cmp = "<"
		}
		// Tuple comparison (sortKey, id) > (arg, AfterID), or the two-clause
		// equivalent below — tiebreak on id ascending regardless of
		// SortDir, per DEC-001.
		conditions = append(conditions, fmt.Sprintf("(%s %s ? OR (%s = ? AND id > ?))", sortKey, cmp, sortKey))
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
		query += fmt.Sprintf(" ORDER BY %s %s, id ASC", sortKey, dir)
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

	var epics []EpicRecord
	for rows.Next() {
		var e EpicRecord
		if err := rows.Scan(&e.ID, &e.Name, &e.Description, &e.Status, &e.Priority, &e.ProjectID, &e.ArchivedAt, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		epics = append(epics, e)
	}
	return epics, rows.Err()
}

// UpdateEpic applies non-nil pointer fields to the epic row.
func (s *Store) UpdateEpic(id string, u EpicUpdate) error {
	var sets []string
	var args []interface{}

	if u.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *u.Name)
	}
	if u.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *u.Description)
	}
	if u.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *u.Status)
	}
	if u.Priority != nil {
		sets = append(sets, "priority = ?")
		args = append(args, *u.Priority)
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

	query := fmt.Sprintf("UPDATE epics SET %s WHERE id = ?", strings.Join(sets, ", "))
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("epic %s not found", id)
	}
	return nil
}

// ArchiveEpic sets archived_at = CURRENT_TIMESTAMP. Idempotent: calling
// archive on an already-archived epic refreshes the timestamp. Does not
// touch status — archiving is orthogonal to the epic's workflow state
// (an archived epic isn't the same fact as the epic being "done").
func (s *Store) ArchiveEpic(id string) error {
	res, err := s.db.Exec(
		`UPDATE epics SET archived_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("epic %s not found", id)
	}
	return nil
}

// UnarchiveEpic clears archived_at, restoring the epic to active
// (in the archive sense; status is untouched).
func (s *Store) UnarchiveEpic(id string) error {
	res, err := s.db.Exec(
		`UPDATE epics SET archived_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("epic %s not found", id)
	}
	return nil
}

// DeleteEpic removes an epic and clears epic_id on associated tasks. The
// reference-nulling UPDATE and the epic's own DELETE run inside a single
// transaction so a failed UPDATE (lock contention, disk full, etc.) can
// never leave the epic deleted while tasks still point at it.
func (s *Store) DeleteEpic(id string) error {
	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear epic_id on any tasks referencing this epic
	if _, err := tx.Exec("UPDATE tasks SET epic_id = NULL WHERE epic_id = ?", id); err != nil {
		return err
	}

	result, err := tx.Exec("DELETE FROM epics WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("epic %s not found", id)
	}
	return tx.Commit()
}

// NextEpicID generates the next sequential epic ID for today.
func (s *Store) NextEpicID() (string, error) {
	date := time.Now().Format("20060102")
	prefix := "EP-" + date + "-"

	var maxSeq int
	err := s.ReadDB().QueryRow(
		"SELECT COALESCE(MAX(CAST(SUBSTR(id, ?) AS INTEGER)), 0) FROM epics WHERE id LIKE ?",
		len(prefix)+1, prefix+"%",
	).Scan(&maxSeq)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%04d", prefix, maxSeq+1), nil
}
