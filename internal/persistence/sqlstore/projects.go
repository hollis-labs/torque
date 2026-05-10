package sqlstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ProjectRecord mirrors the projects table row.
type ProjectRecord struct {
	ID           string
	Name         string
	Description  string
	RepoPath     string
	AgentPath    string
	ReadPaths    sql.NullString
	WritePaths   sql.NullString
	ContextPaths sql.NullString
	Permissions  sql.NullString
	Rules        sql.NullString
	Status       string
	Icon         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ProjectFilter holds optional filter criteria for ListProjects.
type ProjectFilter struct {
	Status string
	Limit  int
	Offset int
}

// ProjectUpdate holds optional fields to update; nil pointer = no change.
type ProjectUpdate struct {
	Name         *string
	Description  *string
	RepoPath     *string
	AgentPath    *string
	ReadPaths    *sql.NullString
	WritePaths   *sql.NullString
	ContextPaths *sql.NullString
	Permissions  *sql.NullString
	Rules        *sql.NullString
	Status       *string
	Icon         *string
}

// CreateProject inserts a new project.
func (s *Store) CreateProject(p *ProjectRecord) error {
	if p.Status == "" {
		p.Status = "active"
	}
	_, err := s.db.Exec(`INSERT INTO projects (
		id, name, description, repo_path, agent_path, read_paths, write_paths, context_paths, permissions, rules, status, icon
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.RepoPath, p.AgentPath, p.ReadPaths, p.WritePaths, p.ContextPaths, p.Permissions, p.Rules, p.Status, p.Icon,
	)
	return err
}

// GetProject fetches a single project by ID.
func (s *Store) GetProject(id string) (*ProjectRecord, error) {
	p := &ProjectRecord{}
	err := s.ReadDB().QueryRow(`SELECT id, name, description, repo_path, agent_path, read_paths, write_paths, context_paths, permissions, rules, status, icon, created_at, updated_at FROM projects WHERE id = ?`, id).Scan(
		&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.AgentPath, &p.ReadPaths, &p.WritePaths, &p.ContextPaths, &p.Permissions, &p.Rules, &p.Status, &p.Icon, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project %s not found", id)
	}
	return p, err
}

// ListProjects returns projects matching the filter, ordered by name.
func (s *Store) ListProjects(f ProjectFilter) ([]ProjectRecord, error) {
	query := `SELECT id, name, description, repo_path, agent_path, read_paths, write_paths, context_paths, permissions, rules, status, icon, created_at, updated_at FROM projects`

	var conditions []string
	var args []interface{}

	if f.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, f.Status)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY name ASC"

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

	var projects []ProjectRecord
	for rows.Next() {
		var p ProjectRecord
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.AgentPath, &p.ReadPaths, &p.WritePaths, &p.ContextPaths, &p.Permissions, &p.Rules, &p.Status, &p.Icon, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// UpdateProject applies non-nil pointer fields to the project row.
func (s *Store) UpdateProject(id string, u ProjectUpdate) error {
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
	if u.RepoPath != nil {
		sets = append(sets, "repo_path = ?")
		args = append(args, *u.RepoPath)
	}
	if u.AgentPath != nil {
		sets = append(sets, "agent_path = ?")
		args = append(args, *u.AgentPath)
	}
	if u.ReadPaths != nil {
		sets = append(sets, "read_paths = ?")
		args = append(args, *u.ReadPaths)
	}
	if u.WritePaths != nil {
		sets = append(sets, "write_paths = ?")
		args = append(args, *u.WritePaths)
	}
	if u.ContextPaths != nil {
		sets = append(sets, "context_paths = ?")
		args = append(args, *u.ContextPaths)
	}
	if u.Permissions != nil {
		sets = append(sets, "permissions = ?")
		args = append(args, *u.Permissions)
	}
	if u.Rules != nil {
		sets = append(sets, "rules = ?")
		args = append(args, *u.Rules)
	}
	if u.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *u.Status)
	}
	if u.Icon != nil {
		sets = append(sets, "icon = ?")
		args = append(args, *u.Icon)
	}

	if len(sets) == 0 {
		return nil
	}

	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)

	query := fmt.Sprintf("UPDATE projects SET %s WHERE id = ?", strings.Join(sets, ", "))
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("project %s not found", id)
	}
	return nil
}

// DeleteProject removes a project and clears project_id on associated tasks, sprints, and epics.
func (s *Store) DeleteProject(id string) error {
	// Clear project_id on any tasks referencing this project
	s.db.Exec("UPDATE tasks SET project_id = NULL WHERE project_id = ?", id)
	// Clear project_id on any sprints referencing this project
	s.db.Exec("UPDATE sprints SET project_id = NULL WHERE project_id = ?", id)
	// Clear project_id on any epics referencing this project
	s.db.Exec("UPDATE epics SET project_id = NULL WHERE project_id = ?", id)
	// Remove project-scoped context artifacts
	s.db.Exec("DELETE FROM project_artifacts WHERE project_id = ?", id)

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
	err := s.ReadDB().QueryRow(
		"SELECT COALESCE(MAX(CAST(SUBSTR(id, ?) AS INTEGER)), 0) FROM projects WHERE id LIKE ?",
		len(prefix)+1, prefix+"%",
	).Scan(&maxSeq)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%04d", prefix, maxSeq+1), nil
}
