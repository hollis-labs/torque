package sqlstore

import (
	"database/sql"
	"fmt"
	"time"
)

// ProjectRecord mirrors the projects table row.
type ProjectRecord struct {
	ID          string
	Name        string
	Description string
	RepoPath    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateProject inserts a new project.
func (s *Store) CreateProject(p *ProjectRecord) error {
	_, err := s.db.Exec(`INSERT INTO projects (id, name, description, repo_path) VALUES (?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.RepoPath,
	)
	return err
}

// GetProject fetches a single project by ID.
func (s *Store) GetProject(id string) (*ProjectRecord, error) {
	p := &ProjectRecord{}
	err := s.db.QueryRow(`SELECT id, name, description, repo_path, created_at, updated_at FROM projects WHERE id = ?`, id).Scan(
		&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project %s not found", id)
	}
	return p, err
}

// ListProjects returns all projects ordered by name.
func (s *Store) ListProjects() ([]ProjectRecord, error) {
	rows, err := s.db.Query(`SELECT id, name, description, repo_path, created_at, updated_at FROM projects ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []ProjectRecord
	for rows.Next() {
		var p ProjectRecord
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// DeleteProject removes a project and clears project_id on associated tasks.
func (s *Store) DeleteProject(id string) error {
	// Clear project_id on any tasks referencing this project
	s.db.Exec("UPDATE tasks SET project_id = NULL WHERE project_id = ?", id)

	result, err := s.db.Exec("DELETE FROM projects WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("project %s not found", id)
	}
	return nil
}

// NextProjectID generates the next sequential project ID for today.
func (s *Store) NextProjectID() (string, error) {
	date := time.Now().Format("20060102")
	prefix := "PRJ-" + date + "-"

	var maxSeq int
	err := s.db.QueryRow(
		"SELECT COALESCE(MAX(CAST(SUBSTR(id, ?) AS INTEGER)), 0) FROM projects WHERE id LIKE ?",
		len(prefix)+1, prefix+"%",
	).Scan(&maxSeq)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%04d", prefix, maxSeq+1), nil
}
