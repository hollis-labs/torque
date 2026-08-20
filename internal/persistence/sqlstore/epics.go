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
	IncludeArchived bool
	Limit           int
	Offset          int
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

// ListEpics returns epics matching the filter, ordered by updated_at DESC.
// Archived rows (archived_at IS NOT NULL) are excluded unless
// f.IncludeArchived is true.
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

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY updated_at DESC"

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
