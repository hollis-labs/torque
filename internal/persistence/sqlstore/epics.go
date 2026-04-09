package sqlstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// EpicRecord mirrors the epics table row.
type EpicRecord struct {
	ID          string
	Name        string
	Description string
	Status      string
	Priority    sql.NullInt64
	ProjectID   sql.NullString
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// EpicFilter holds optional filter criteria for ListEpics.
type EpicFilter struct {
	Status    string
	ProjectID string
	Limit     int
	Offset    int
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
	err := s.db.QueryRow(`SELECT id, name, description, status, priority, project_id, created_at, updated_at FROM epics WHERE id = ?`, id).Scan(
		&e.ID, &e.Name, &e.Description, &e.Status, &e.Priority, &e.ProjectID, &e.CreatedAt, &e.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("epic %s not found", id)
	}
	return e, err
}

// ListEpics returns epics matching the filter, ordered by created_at DESC.
func (s *Store) ListEpics(f EpicFilter) ([]EpicRecord, error) {
	query := `SELECT id, name, description, status, priority, project_id, created_at, updated_at FROM epics`

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

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var epics []EpicRecord
	for rows.Next() {
		var e EpicRecord
		if err := rows.Scan(&e.ID, &e.Name, &e.Description, &e.Status, &e.Priority, &e.ProjectID, &e.CreatedAt, &e.UpdatedAt); err != nil {
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
		args = append(args, *u.ProjectID)
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

// DeleteEpic removes an epic and clears epic_id on associated tasks.
func (s *Store) DeleteEpic(id string) error {
	// Clear epic_id on any tasks referencing this epic
	s.db.Exec("UPDATE tasks SET epic_id = NULL WHERE epic_id = ?", id)

	result, err := s.db.Exec("DELETE FROM epics WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("epic %s not found", id)
	}
	return nil
}

// NextEpicID generates the next sequential epic ID for today.
func (s *Store) NextEpicID() (string, error) {
	date := time.Now().Format("20060102")
	prefix := "EP-" + date + "-"

	var maxSeq int
	err := s.db.QueryRow(
		"SELECT COALESCE(MAX(CAST(SUBSTR(id, ?) AS INTEGER)), 0) FROM epics WHERE id LIKE ?",
		len(prefix)+1, prefix+"%",
	).Scan(&maxSeq)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%04d", prefix, maxSeq+1), nil
}
