package sqlstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ProjectRecord mirrors the projects table row. ArchivedAt is NULL for
// active projects; non-NULL means the project is archived (kept for
// audit, hidden from active filters). Archiving is independent of Status.
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
	ArchivedAt   sql.NullTime
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ProjectFilter holds optional filter criteria for ListProjects.
// IncludeArchived defaults to false: archived rows (archived_at IS NOT
// NULL) are excluded unless the caller explicitly asks for them.
//
// SortBy/SortDir/AfterSortValue/AfterID are Phase 4's ENT-PROJECT adoption
// of PRIM-001 (cursor pagination) / PRIM-002 (sort_by), mirroring Task's
// reference implementation (see ListTasks's doc comment). SortBy == ""
// preserves the original hardcoded `ORDER BY name ASC` for callers that
// haven't adopted the primitive (HTTP /api/v1/projects, scheduler
// internals); the MCP layer (torque_project_list) always resolves a
// default SortBy/SortDir before calling ListProjects.
type ProjectFilter struct {
	Status          string
	IncludeArchived bool
	Limit           int
	Offset          int
	SortBy          string
	SortDir         string
	AfterSortValue  string
	AfterID         string
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
	err := s.ReadDB().QueryRow(`SELECT id, name, description, repo_path, agent_path, read_paths, write_paths, context_paths, permissions, rules, status, icon, archived_at, created_at, updated_at FROM projects WHERE id = ?`, id).Scan(
		&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.AgentPath, &p.ReadPaths, &p.WritePaths, &p.ContextPaths, &p.Permissions, &p.Rules, &p.Status, &p.Icon, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project %s not found", id)
	}
	return p, err
}

// projectSortColumn maps a sort_by value to its backing SQL column for
// ListProjects (PRIM-002). Mirrors taskSortColumn's contract: the MCP layer
// validates sort_by against an explicit allow-list
// (internal/service/pagination.ValidateSortBy) before this is ever reached,
// but ListProjects stays defensive — an empty or unrecognized sort_by both
// return "", which ListProjects treats as "no sort_by supplied" and falls
// back to the original hardcoded `name ASC` order.
func projectSortColumn(sortBy string) string {
	switch sortBy {
	case "name", "status", "updated_at", "created_at":
		return sortBy
	default:
		return ""
	}
}

// projectCursorArg converts a cursor's string-encoded sort value (DEC-001's
// `sv` field) into the SQL bind argument for sortBy's column. Unlike
// taskCursorArg, every Project sort column is TEXT-typed (name/status are
// plain strings; updated_at/created_at are validated against
// SQLiteDatetimeLayout but bound as the original string — see
// taskCursorArg's doc comment for why binding the raw string instead of a
// re-parsed time.Time matters), so there's no numeric-column case to handle.
func projectCursorArg(sortBy, sv string) (any, error) {
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

// ListProjects returns projects matching the filter. Default order
// (f.SortBy == "") is name ASC. When f.SortBy is set (PRIM-002), order
// becomes `<sort column> <SortDir>, id ASC` and, if f.AfterID is also set,
// results are additionally filtered to rows after the cursor's (sort
// value, id) position (PRIM-001/DEC-001 keyset pagination) — see
// ListTasks for the reference implementation this mirrors. Archived rows
// (archived_at IS NOT NULL) are excluded unless f.IncludeArchived is true.
func (s *Store) ListProjects(f ProjectFilter) ([]ProjectRecord, error) {
	query := `SELECT id, name, description, repo_path, agent_path, read_paths, write_paths, context_paths, permissions, rules, status, icon, archived_at, created_at, updated_at FROM projects`

	var conditions []string
	var args []interface{}

	if f.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, f.Status)
	}
	if !f.IncludeArchived {
		conditions = append(conditions, "archived_at IS NULL")
	}

	sortCol := projectSortColumn(f.SortBy)
	desc := strings.EqualFold(f.SortDir, "desc")
	if sortCol != "" && f.AfterID != "" {
		arg, err := projectCursorArg(f.SortBy, f.AfterSortValue)
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
		query += " ORDER BY name ASC"
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

	var projects []ProjectRecord
	for rows.Next() {
		var p ProjectRecord
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.AgentPath, &p.ReadPaths, &p.WritePaths, &p.ContextPaths, &p.Permissions, &p.Rules, &p.Status, &p.Icon, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

// ArchiveProject sets archived_at = CURRENT_TIMESTAMP. Idempotent: calling
// archive on an already-archived project refreshes the timestamp. Does not
// touch status — archiving is orthogonal to the project's active/inactive
// workflow state.
func (s *Store) ArchiveProject(id string) error {
	res, err := s.db.Exec(
		`UPDATE projects SET archived_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("project %s not found", id)
	}
	return nil
}

// UnarchiveProject clears archived_at, restoring the project to active
// (in the archive sense; status is untouched).
func (s *Store) UnarchiveProject(id string) error {
	res, err := s.db.Exec(
		`UPDATE projects SET archived_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("project %s not found", id)
	}
	return nil
}

// DeleteProject removes a project and clears project_id on associated
// tasks, sprints, and epics. The reference-nulling UPDATEs, the
// project-artifact cleanup, and the project's own DELETE all run inside a
// single transaction so a failure partway through (lock contention, disk
// full, etc.) can never leave the project deleted while other rows still
// point at it.
func (s *Store) DeleteProject(id string) error {
	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear project_id on any tasks referencing this project
	if _, err := tx.Exec("UPDATE tasks SET project_id = NULL WHERE project_id = ?", id); err != nil {
		return err
	}
	// Clear project_id on any sprints referencing this project
	if _, err := tx.Exec("UPDATE sprints SET project_id = NULL WHERE project_id = ?", id); err != nil {
		return err
	}
	// Clear project_id on any epics referencing this project
	if _, err := tx.Exec("UPDATE epics SET project_id = NULL WHERE project_id = ?", id); err != nil {
		return err
	}
	// Remove project-scoped context artifacts
	if _, err := tx.Exec("DELETE FROM project_artifacts WHERE project_id = ?", id); err != nil {
		return err
	}

	result, err := tx.Exec("DELETE FROM projects WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("project %s not found", id)
	}
	return tx.Commit()
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
