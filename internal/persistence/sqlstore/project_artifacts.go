package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrProjectArtifactNotFound = errors.New("project artifact not found")

type ProjectArtifactRecord struct {
	ID          int64
	ProjectID   string
	EntryType   string
	Title       string
	Description string
	FilePath    string
	URL         string
	Content     string
	Permissions sql.NullString
	Rules       sql.NullString
	Metadata    sql.NullString
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ProjectArtifactUpdate struct {
	EntryType   *string
	Title       *string
	Description *string
	FilePath    *string
	URL         *string
	Content     *string
	Permissions *sql.NullString
	Rules       *sql.NullString
	Metadata    *sql.NullString
}

const projectArtifactSelectCols = `id, project_id, entry_type, title, description, file_path, url, content, permissions, rules, metadata, created_at, updated_at`

func (s *Store) CreateProjectArtifact(a *ProjectArtifactRecord) error {
	if a.EntryType == "" {
		a.EntryType = "document"
	}
	const q = `INSERT INTO project_artifacts (
		project_id, entry_type, title, description, file_path, url, content, permissions, rules, metadata
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.Exec(q,
		a.ProjectID, a.EntryType, a.Title, a.Description, a.FilePath, a.URL, a.Content, a.Permissions, a.Rules, a.Metadata,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	a.ID = id
	return nil
}

func (s *Store) GetProjectArtifact(id int64) (*ProjectArtifactRecord, error) {
	row := s.db.QueryRow(`SELECT `+projectArtifactSelectCols+` FROM project_artifacts WHERE id = ?`, id)
	var a ProjectArtifactRecord
	if err := row.Scan(
		&a.ID, &a.ProjectID, &a.EntryType, &a.Title, &a.Description, &a.FilePath, &a.URL, &a.Content,
		&a.Permissions, &a.Rules, &a.Metadata, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project artifact %d: %w", id, ErrProjectArtifactNotFound)
		}
		return nil, err
	}
	return &a, nil
}

func (s *Store) ListProjectArtifacts(projectID string) ([]ProjectArtifactRecord, error) {
	rows, err := s.db.Query(`SELECT `+projectArtifactSelectCols+` FROM project_artifacts WHERE project_id = ? ORDER BY file_path ASC, created_at ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProjectArtifactRecord
	for rows.Next() {
		var a ProjectArtifactRecord
		if err := rows.Scan(
			&a.ID, &a.ProjectID, &a.EntryType, &a.Title, &a.Description, &a.FilePath, &a.URL, &a.Content,
			&a.Permissions, &a.Rules, &a.Metadata, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateProjectArtifact(id int64, u ProjectArtifactUpdate) error {
	sets := make([]string, 0, 9)
	args := make([]any, 0, 10)
	if u.EntryType != nil {
		sets = append(sets, "entry_type = ?")
		args = append(args, *u.EntryType)
	}
	if u.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *u.Title)
	}
	if u.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *u.Description)
	}
	if u.FilePath != nil {
		sets = append(sets, "file_path = ?")
		args = append(args, *u.FilePath)
	}
	if u.URL != nil {
		sets = append(sets, "url = ?")
		args = append(args, *u.URL)
	}
	if u.Content != nil {
		sets = append(sets, "content = ?")
		args = append(args, *u.Content)
	}
	if u.Permissions != nil {
		sets = append(sets, "permissions = ?")
		args = append(args, *u.Permissions)
	}
	if u.Rules != nil {
		sets = append(sets, "rules = ?")
		args = append(args, *u.Rules)
	}
	if u.Metadata != nil {
		sets = append(sets, "metadata = ?")
		args = append(args, *u.Metadata)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	res, err := s.db.Exec(`UPDATE project_artifacts SET `+joinComma(sets)+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("project artifact %d: %w", id, ErrProjectArtifactNotFound)
	}
	return nil
}

func (s *Store) DeleteProjectArtifact(id int64) error {
	res, err := s.db.Exec(`DELETE FROM project_artifacts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("project artifact %d: %w", id, ErrProjectArtifactNotFound)
	}
	return nil
}

func joinComma(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += ", " + parts[i]
	}
	return out
}
