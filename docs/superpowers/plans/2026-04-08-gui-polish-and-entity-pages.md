# GUI Polish & Entity Pages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enrich backend models (Project/Sprint/Epic), add REST API endpoints, and build GUI pages for all four entity types (Tasks, Projects, Sprints, Epics) with shared components matching Fragments Engine design parity.

**Architecture:** Migration adds fields to existing tables. New HTTP handler files follow the existing tasks.go pattern. GUI uses composition — shared `EntityPage` layout, `SummaryCards`, `EntityTable`, and `EntityDetailLayout` components composed per entity. Props down, events up. All shadcn, no custom components.

**Tech Stack:** Go 1.26 / SQLite / Chi router (backend), React 19 / Vite / Tailwind CSS 4 / shadcn/ui / TypeScript (frontend)

---

## File Map

### Backend — Model Enrichment
- Create: `internal/persistence/sqlstore/migrations/004_enrich_entities.sql`
- Modify: `internal/persistence/sqlstore/sprints.go` — add project_id field, drop goal
- Modify: `internal/persistence/sqlstore/projects.go` — add status, icon fields
- Modify: `internal/persistence/sqlstore/epics.go` — add priority, project_id fields
- Modify: `internal/persistence/sqlstore/sprints_test.go` — update for new fields
- Modify: `internal/persistence/sqlstore/projects_test.go` — update for new fields
- Modify: `internal/persistence/sqlstore/epics_test.go` — update for new fields

### Backend — Service Layer Updates
- Modify: `internal/service/sprint.go` — update create/update inputs, simplify status FSM
- Modify: `internal/service/project.go` — add update method, status field
- Modify: `internal/service/epic.go` — update inputs for priority/project_id, simplify statuses
- Modify: `internal/service/sprint_test.go` — update tests for new model
- Modify: `internal/service/project_test.go` — update tests for new model
- Modify: `internal/service/epic_test.go` — update tests for new model

### Backend — HTTP Endpoints
- Create: `internal/httpserver/sprints.go` — CRUD + list + transition handlers
- Create: `internal/httpserver/projects.go` — CRUD + list handlers
- Create: `internal/httpserver/epics.go` — CRUD + list handlers
- Modify: `internal/httpserver/server.go` — register new routes
- Modify: `internal/httpserver/server_test.go` — integration tests for new endpoints

### Frontend — Shared Components
- Create: `apps/gui/src/components/domain/summary-cards.tsx` — reusable stat cards
- Create: `apps/gui/src/components/domain/entity-table.tsx` — generic sortable table
- Create: `apps/gui/src/components/domain/page-header.tsx` — page title + context selector area
- Create: `apps/gui/src/components/domain/detail-header.tsx` — detail page header with breadcrumb
- Create: `apps/gui/src/components/domain/copyable-id.tsx` — click-to-copy ID badge
- Create: `apps/gui/src/components/domain/row-actions.tsx` — "..." dropdown for table rows
- Create: `apps/gui/src/components/domain/progress-bar.tsx` — thin progress indicator

### Frontend — API & Types
- Modify: `apps/gui/src/lib/types.ts` — add Sprint, Project, Epic types
- Modify: `apps/gui/src/lib/api.ts` — add CRUD methods for all 3 entities
- Modify: `apps/gui/src/lib/constants.ts` — add entity status colors, container statuses

### Frontend — Pages
- Create: `apps/gui/src/pages/ProjectsPage.tsx` — projects list with summary cards
- Create: `apps/gui/src/pages/ProjectDetailPage.tsx` — project detail with sprint/task tabs
- Create: `apps/gui/src/pages/SprintsPage.tsx` — sprints list with summary cards
- Create: `apps/gui/src/pages/SprintDetailPage.tsx` — sprint detail with task list
- Create: `apps/gui/src/pages/EpicsPage.tsx` — epics list with summary cards
- Create: `apps/gui/src/pages/EpicDetailPage.tsx` — epic detail with task list
- Modify: `apps/gui/src/pages/BoardPage.tsx` — add summary cards, context selector
- Modify: `apps/gui/src/pages/TaskDetailPage.tsx` — align with Fragments Engine detail layout
- Modify: `apps/gui/src/App.tsx` — add routes + nav items for new pages

---

## Task 1: Database Migration — Enrich Entity Tables

**Files:**
- Create: `internal/persistence/sqlstore/migrations/004_enrich_entities.sql`

- [ ] **Step 1: Write the migration SQL**

```sql
-- Enrich projects: add status and icon
ALTER TABLE projects ADD COLUMN status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE projects ADD COLUMN icon TEXT NOT NULL DEFAULT '';

-- Enrich sprints: add project_id, drop goal (leave column, stop using it)
ALTER TABLE sprints ADD COLUMN project_id TEXT;

-- Enrich epics: add priority and project_id
ALTER TABLE epics ADD COLUMN priority INTEGER;
ALTER TABLE epics ADD COLUMN project_id TEXT;
```

Note: SQLite does not support DROP COLUMN in older versions. We leave `goal` in the schema but stop reading/writing it. The column will be ignored.

- [ ] **Step 2: Run tests to verify migration applies cleanly**

Run: `cd /Users/chrispian/Projects-apps/torque && go test ./internal/persistence/sqlstore/... -run TestCreate -count=1 -v`
Expected: All existing tests still PASS (migration adds columns with defaults, no breaking changes)

- [ ] **Step 3: Commit**

```bash
git add internal/persistence/sqlstore/migrations/004_enrich_entities.sql
git commit -m "feat: add migration 004 to enrich project/sprint/epic tables"
```

---

## Task 2: Update Store Layer — Projects

**Files:**
- Modify: `internal/persistence/sqlstore/projects.go`
- Modify: `internal/persistence/sqlstore/projects_test.go`

- [ ] **Step 1: Write failing tests for new fields**

Add to `projects_test.go`:

```go
func TestCreateProjectWithStatusAndIcon(t *testing.T) {
	store := setupTestStore(t)

	err := store.CreateProject(&sqlstore.ProjectRecord{
		ID:     "PRJ-20260408-0001",
		Name:   "Test Project",
		Status: "active",
		Icon:   "TP",
	})
	require.NoError(t, err)

	got, err := store.GetProject("PRJ-20260408-0001")
	require.NoError(t, err)
	assert.Equal(t, "active", got.Status)
	assert.Equal(t, "TP", got.Icon)
}

func TestUpdateProject(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{
		ID:   "PRJ-20260408-0001",
		Name: "Test Project",
	})

	err := store.UpdateProject("PRJ-20260408-0001", sqlstore.ProjectUpdate{
		Name:   strPtr("Renamed Project"),
		Status: strPtr("inactive"),
		Icon:   strPtr("RP"),
	})
	require.NoError(t, err)

	got, err := store.GetProject("PRJ-20260408-0001")
	require.NoError(t, err)
	assert.Equal(t, "Renamed Project", got.Name)
	assert.Equal(t, "inactive", got.Status)
	assert.Equal(t, "RP", got.Icon)
}

func TestListProjectsFilterByStatus(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-1", Name: "Active", Status: "active"})
	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-2", Name: "Inactive", Status: "inactive"})

	projects, err := store.ListProjects(sqlstore.ProjectFilter{Status: "active"})
	require.NoError(t, err)
	assert.Len(t, projects, 1)
	assert.Equal(t, "Active", projects[0].Name)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/persistence/sqlstore/... -run "TestCreateProjectWithStatusAndIcon|TestUpdateProject|TestListProjectsFilterByStatus" -count=1 -v`
Expected: FAIL — `ProjectUpdate` type not defined, `ListProjects` signature mismatch

- [ ] **Step 3: Update ProjectRecord, add ProjectFilter and ProjectUpdate types**

Update `projects.go`:

```go
// ProjectRecord mirrors the projects table row.
type ProjectRecord struct {
	ID          string
	Name        string
	Description string
	RepoPath    string
	Status      string
	Icon        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProjectFilter holds optional filter criteria for ListProjects.
type ProjectFilter struct {
	Status string
	Limit  int
	Offset int
}

// ProjectUpdate holds optional fields to update; nil pointer = no change.
type ProjectUpdate struct {
	Name        *string
	Description *string
	RepoPath    *string
	Status      *string
	Icon        *string
}
```

- [ ] **Step 4: Update CreateProject to include new fields**

```go
func (s *Store) CreateProject(p *ProjectRecord) error {
	if p.Status == "" {
		p.Status = "active"
	}
	_, err := s.db.Exec(`INSERT INTO projects (id, name, description, repo_path, status, icon) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.RepoPath, p.Status, p.Icon,
	)
	return err
}
```

- [ ] **Step 5: Update GetProject to scan new fields**

```go
func (s *Store) GetProject(id string) (*ProjectRecord, error) {
	p := &ProjectRecord{}
	err := s.db.QueryRow(`SELECT id, name, description, repo_path, status, icon, created_at, updated_at FROM projects WHERE id = ?`, id).Scan(
		&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.Status, &p.Icon, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project %s not found", id)
	}
	return p, err
}
```

- [ ] **Step 6: Update ListProjects to accept ProjectFilter**

```go
func (s *Store) ListProjects(f ProjectFilter) ([]ProjectRecord, error) {
	query := `SELECT id, name, description, repo_path, status, icon, created_at, updated_at FROM projects`

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

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []ProjectRecord
	for rows.Next() {
		var p ProjectRecord
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.Status, &p.Icon, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}
```

- [ ] **Step 7: Add UpdateProject method**

```go
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
```

- [ ] **Step 8: Fix existing callers of ListProjects()**

The service layer calls `s.store.ListProjects()` with no args. Update `internal/service/project.go`:

```go
func (s *ProjectService) List(status string) ([]sqlstore.ProjectRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	return s.store.ListProjects(sqlstore.ProjectFilter{Status: status})
}
```

Also update `DeleteProject` to clear project_id on sprints and epics:

```go
func (s *Store) DeleteProject(id string) error {
	s.db.Exec("UPDATE tasks SET project_id = NULL WHERE project_id = ?", id)
	s.db.Exec("UPDATE sprints SET project_id = NULL WHERE project_id = ?", id)
	s.db.Exec("UPDATE epics SET project_id = NULL WHERE project_id = ?", id)

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
```

- [ ] **Step 9: Add `"strings"` import to projects.go** (needed for `strings.Join`)

- [ ] **Step 10: Run all tests**

Run: `go test ./internal/persistence/sqlstore/... -count=1 -v`
Expected: ALL PASS

- [ ] **Step 11: Commit**

```bash
git add internal/persistence/sqlstore/projects.go internal/persistence/sqlstore/projects_test.go internal/service/project.go
git commit -m "feat: enrich project model with status, icon, update, and filtered list"
```

---

## Task 3: Update Store Layer — Sprints

**Files:**
- Modify: `internal/persistence/sqlstore/sprints.go`
- Modify: `internal/persistence/sqlstore/sprints_test.go`

- [ ] **Step 1: Write failing tests for project_id and simplified status**

Add to `sprints_test.go`:

```go
func TestCreateSprintWithProjectID(t *testing.T) {
	store := setupTestStore(t)

	// Create a project first
	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-1", Name: "Test Project"})

	err := store.CreateSprint(&sqlstore.SprintRecord{
		ID:        "SP-20260408-0001",
		Name:      "Sprint 1",
		ProjectID: sql.NullString{String: "PRJ-1", Valid: true},
	})
	require.NoError(t, err)

	got, err := store.GetSprint("SP-20260408-0001")
	require.NoError(t, err)
	assert.True(t, got.ProjectID.Valid)
	assert.Equal(t, "PRJ-1", got.ProjectID.String)
}

func TestListSprintsFilterByProjectID(t *testing.T) {
	store := setupTestStore(t)

	store.CreateSprint(&sqlstore.SprintRecord{
		ID:        "SP-1",
		Name:      "In Project",
		ProjectID: sql.NullString{String: "PRJ-1", Valid: true},
	})
	store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-2", Name: "No Project"})

	sprints, err := store.ListSprints(sqlstore.SprintFilter{ProjectID: "PRJ-1"})
	require.NoError(t, err)
	assert.Len(t, sprints, 1)
	assert.Equal(t, "In Project", sprints[0].Name)
}

func TestUpdateSprintProjectID(t *testing.T) {
	store := setupTestStore(t)

	store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-1", Name: "Sprint 1"})

	projectID := "PRJ-1"
	err := store.UpdateSprint("SP-1", sqlstore.SprintUpdate{
		ProjectID: &projectID,
	})
	require.NoError(t, err)

	got, err := store.GetSprint("SP-1")
	require.NoError(t, err)
	assert.True(t, got.ProjectID.Valid)
	assert.Equal(t, "PRJ-1", got.ProjectID.String)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/persistence/sqlstore/... -run "TestCreateSprintWithProjectID|TestListSprintsFilterByProjectID|TestUpdateSprintProjectID" -count=1 -v`
Expected: FAIL — `ProjectID` field not on SprintRecord

- [ ] **Step 3: Update SprintRecord to add ProjectID, keep Goal for backward compat**

```go
type SprintRecord struct {
	ID           string
	Name         string
	Goal         string          // deprecated — kept for schema compat, not used in new code
	Status       string
	ApprovalMode string
	CostBudget   sql.NullFloat64
	ProjectID    sql.NullString
	StartedAt    sql.NullTime
	EndedAt      sql.NullTime
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
```

- [ ] **Step 4: Update SprintFilter to add ProjectID**

```go
type SprintFilter struct {
	Status    string
	ProjectID string
	Limit     int
	Offset    int
}
```

- [ ] **Step 5: Update SprintUpdate to add ProjectID**

```go
type SprintUpdate struct {
	Name         *string
	Goal         *string // deprecated
	ApprovalMode *string
	CostBudget   *float64
	ProjectID    *string
}
```

- [ ] **Step 6: Update CreateSprint to include project_id**

```go
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
```

- [ ] **Step 7: Update GetSprint to scan project_id**

```go
func (s *Store) GetSprint(id string) (*SprintRecord, error) {
	sp := &SprintRecord{}
	err := s.db.QueryRow(`SELECT
		id, name, goal, status, approval_mode, cost_budget, project_id,
		started_at, ended_at, created_at, updated_at
	FROM sprints WHERE id = ?`, id).Scan(
		&sp.ID, &sp.Name, &sp.Goal, &sp.Status, &sp.ApprovalMode,
		&sp.CostBudget, &sp.ProjectID, &sp.StartedAt, &sp.EndedAt,
		&sp.CreatedAt, &sp.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("sprint %s not found", id)
	}
	return sp, err
}
```

- [ ] **Step 8: Update ListSprints to filter by project_id and scan it**

```go
func (s *Store) ListSprints(f SprintFilter) ([]SprintRecord, error) {
	query := `SELECT id, name, goal, status, approval_mode, cost_budget, project_id,
		started_at, ended_at, created_at, updated_at FROM sprints`

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

	var sprints []SprintRecord
	for rows.Next() {
		var sp SprintRecord
		if err := rows.Scan(
			&sp.ID, &sp.Name, &sp.Goal, &sp.Status, &sp.ApprovalMode,
			&sp.CostBudget, &sp.ProjectID, &sp.StartedAt, &sp.EndedAt,
			&sp.CreatedAt, &sp.UpdatedAt,
		); err != nil {
			return nil, err
		}
		sprints = append(sprints, sp)
	}
	return sprints, rows.Err()
}
```

- [ ] **Step 9: Update UpdateSprint to handle project_id**

Add to the existing UpdateSprint method, after the CostBudget block:

```go
	if u.ProjectID != nil {
		sets = append(sets, "project_id = ?")
		args = append(args, *u.ProjectID)
	}
```

- [ ] **Step 10: Run all store tests**

Run: `go test ./internal/persistence/sqlstore/... -count=1 -v`
Expected: ALL PASS

- [ ] **Step 11: Commit**

```bash
git add internal/persistence/sqlstore/sprints.go internal/persistence/sqlstore/sprints_test.go
git commit -m "feat: enrich sprint model with project_id, update default status to active"
```

---

## Task 4: Update Store Layer — Epics

**Files:**
- Modify: `internal/persistence/sqlstore/epics.go`
- Modify: `internal/persistence/sqlstore/epics_test.go`

- [ ] **Step 1: Write failing tests for priority and project_id**

Add to `epics_test.go`:

```go
func TestCreateEpicWithPriorityAndProjectID(t *testing.T) {
	store := setupTestStore(t)

	err := store.CreateEpic(&sqlstore.EpicRecord{
		ID:        "EP-20260408-0001",
		Name:      "Epic 1",
		Priority:  sql.NullInt64{Int64: 1, Valid: true},
		ProjectID: sql.NullString{String: "PRJ-1", Valid: true},
	})
	require.NoError(t, err)

	got, err := store.GetEpic("EP-20260408-0001")
	require.NoError(t, err)
	assert.True(t, got.Priority.Valid)
	assert.Equal(t, int64(1), got.Priority.Int64)
	assert.True(t, got.ProjectID.Valid)
	assert.Equal(t, "PRJ-1", got.ProjectID.String)
}

func TestListEpicsFilterByProjectID(t *testing.T) {
	store := setupTestStore(t)

	store.CreateEpic(&sqlstore.EpicRecord{
		ID:        "EP-1",
		Name:      "In Project",
		ProjectID: sql.NullString{String: "PRJ-1", Valid: true},
	})
	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-2", Name: "No Project"})

	epics, err := store.ListEpics(sqlstore.EpicFilter{ProjectID: "PRJ-1"})
	require.NoError(t, err)
	assert.Len(t, epics, 1)
	assert.Equal(t, "In Project", epics[0].Name)
}

func TestUpdateEpicPriorityAndProjectID(t *testing.T) {
	store := setupTestStore(t)

	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-1", Name: "Epic 1"})

	priority := int64(2)
	projectID := "PRJ-1"
	err := store.UpdateEpic("EP-1", sqlstore.EpicUpdate{
		Priority:  &priority,
		ProjectID: &projectID,
	})
	require.NoError(t, err)

	got, err := store.GetEpic("EP-1")
	require.NoError(t, err)
	assert.True(t, got.Priority.Valid)
	assert.Equal(t, int64(2), got.Priority.Int64)
	assert.True(t, got.ProjectID.Valid)
	assert.Equal(t, "PRJ-1", got.ProjectID.String)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/persistence/sqlstore/... -run "TestCreateEpicWithPriority|TestListEpicsFilterByProjectID|TestUpdateEpicPriority" -count=1 -v`
Expected: FAIL — fields not on EpicRecord

- [ ] **Step 3: Update EpicRecord**

```go
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
```

- [ ] **Step 4: Update EpicFilter**

```go
type EpicFilter struct {
	Status    string
	ProjectID string
	Limit     int
	Offset    int
}
```

- [ ] **Step 5: Update EpicUpdate**

```go
type EpicUpdate struct {
	Name        *string
	Description *string
	Status      *string
	Priority    *int64
	ProjectID   *string
}
```

- [ ] **Step 6: Update CreateEpic**

```go
func (s *Store) CreateEpic(e *EpicRecord) error {
	if e.Status == "" {
		e.Status = "active"
	}

	_, err := s.db.Exec(`INSERT INTO epics (id, name, description, status, priority, project_id) VALUES (?, ?, ?, ?, ?, ?)`,
		e.ID, e.Name, e.Description, e.Status, e.Priority, e.ProjectID,
	)
	return err
}
```

- [ ] **Step 7: Update GetEpic to scan new fields**

```go
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
```

- [ ] **Step 8: Update ListEpics to filter by project_id and scan new fields**

```go
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
```

- [ ] **Step 9: Update UpdateEpic to handle priority and project_id**

Add to existing UpdateEpic method:

```go
	if u.Priority != nil {
		sets = append(sets, "priority = ?")
		args = append(args, *u.Priority)
	}
	if u.ProjectID != nil {
		sets = append(sets, "project_id = ?")
		args = append(args, *u.ProjectID)
	}
```

- [ ] **Step 10: Run all store tests**

Run: `go test ./internal/persistence/sqlstore/... -count=1 -v`
Expected: ALL PASS

- [ ] **Step 11: Commit**

```bash
git add internal/persistence/sqlstore/epics.go internal/persistence/sqlstore/epics_test.go
git commit -m "feat: enrich epic model with priority, project_id, default status active"
```

---

## Task 5: Update Service Layer — All Three Entities

**Files:**
- Modify: `internal/service/sprint.go`
- Modify: `internal/service/project.go`
- Modify: `internal/service/epic.go`
- Modify: `internal/service/sprint_test.go`
- Modify: `internal/service/project_test.go`
- Modify: `internal/service/epic_test.go`

- [ ] **Step 1: Update SprintCreateInput — drop Goal, add ProjectID, simplify status**

In `sprint.go`, update the input struct and the status FSM:

```go
type SprintCreateInput struct {
	Name         string
	ApprovalMode string
	CostBudget   *float64
	ProjectID    string // optional
}

// Simplified status: active/inactive (containers are on or off)
var validSprintTransitions = map[string][]string{
	"active":   {"inactive"},
	"inactive": {"active"},
}
```

- [ ] **Step 2: Update SprintService.Create to use new input**

```go
func (s *SprintService) Create(input SprintCreateInput) (*sqlstore.SprintRecord, error) {
	if err := s.feature.Require("sprints"); err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
	}
	if input.ApprovalMode == "" {
		input.ApprovalMode = "approve_each"
	}
	if !validApprovalModes[input.ApprovalMode] {
		return nil, &ValidationError{
			Field:   "approval_mode",
			Message: "must be one of: auto, approve_sprint, approve_each",
		}
	}

	id, err := s.store.NextSprintID()
	if err != nil {
		return nil, err
	}

	record := &sqlstore.SprintRecord{
		ID:           id,
		Name:         input.Name,
		Status:       "active",
		ApprovalMode: input.ApprovalMode,
	}

	if input.CostBudget != nil {
		record.CostBudget = sql.NullFloat64{Float64: *input.CostBudget, Valid: true}
	}
	if input.ProjectID != "" {
		record.ProjectID = sql.NullString{String: input.ProjectID, Valid: true}
	}

	if err := s.store.CreateSprint(record); err != nil {
		return nil, err
	}

	return s.store.GetSprint(id)
}
```

- [ ] **Step 3: Update SprintService.List to accept project_id filter**

```go
func (s *SprintService) List(status, projectID string) ([]sqlstore.SprintRecord, error) {
	if err := s.feature.Require("sprints"); err != nil {
		return nil, err
	}
	return s.store.ListSprints(sqlstore.SprintFilter{Status: status, ProjectID: projectID})
}
```

- [ ] **Step 4: Update ProjectService — add Update method, change List signature**

In `project.go`:

```go
type ProjectCreateInput struct {
	Name        string
	Description string
	RepoPath    string
	Icon        string
}

func (s *ProjectService) Create(input ProjectCreateInput) (*sqlstore.ProjectRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
	}

	id, err := s.store.NextProjectID()
	if err != nil {
		return nil, err
	}

	record := &sqlstore.ProjectRecord{
		ID:          id,
		Name:        input.Name,
		Description: input.Description,
		RepoPath:    input.RepoPath,
		Status:      "active",
		Icon:        input.Icon,
	}

	if err := s.store.CreateProject(record); err != nil {
		return nil, err
	}

	return s.store.GetProject(id)
}

func (s *ProjectService) List(status string) ([]sqlstore.ProjectRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	return s.store.ListProjects(sqlstore.ProjectFilter{Status: status})
}

func (s *ProjectService) Update(id string, update sqlstore.ProjectUpdate) error {
	if err := s.feature.Require("projects"); err != nil {
		return err
	}
	if update.Status != nil {
		if *update.Status != "active" && *update.Status != "inactive" {
			return &ValidationError{
				Field:   "status",
				Message: "must be one of: active, inactive",
			}
		}
	}
	return s.store.UpdateProject(id, update)
}
```

- [ ] **Step 5: Update EpicService — simplify statuses, add ProjectID**

In `epic.go`:

```go
type EpicCreateInput struct {
	Name        string
	Description string
	Priority    *int64
	ProjectID   string // optional
}

type EpicUpdateInput struct {
	Name        *string
	Description *string
	Status      *string
	Priority    *int64
	ProjectID   *string
}

var validEpicStatuses = map[string]bool{
	"active":   true,
	"inactive": true,
}
```

Update `Create`:

```go
func (s *EpicService) Create(input EpicCreateInput) (*sqlstore.EpicRecord, error) {
	if err := s.feature.Require("epics"); err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
	}

	id, err := s.store.NextEpicID()
	if err != nil {
		return nil, err
	}

	record := &sqlstore.EpicRecord{
		ID:          id,
		Name:        input.Name,
		Description: input.Description,
		Status:      "active",
	}
	if input.Priority != nil {
		record.Priority = sql.NullInt64{Int64: *input.Priority, Valid: true}
	}
	if input.ProjectID != "" {
		record.ProjectID = sql.NullString{String: input.ProjectID, Valid: true}
	}

	if err := s.store.CreateEpic(record); err != nil {
		return nil, err
	}

	return s.store.GetEpic(id)
}
```

Update `Update`:

```go
func (s *EpicService) Update(id string, input EpicUpdateInput) error {
	if err := s.feature.Require("epics"); err != nil {
		return err
	}

	if input.Status != nil && !validEpicStatuses[*input.Status] {
		return &ValidationError{
			Field:   "status",
			Message: "must be one of: active, inactive",
		}
	}

	update := sqlstore.EpicUpdate{
		Name:        input.Name,
		Description: input.Description,
		Status:      input.Status,
		Priority:    input.Priority,
		ProjectID:   input.ProjectID,
	}
	return s.store.UpdateEpic(id, update)
}
```

Update `List`:

```go
func (s *EpicService) List(status, projectID string) ([]sqlstore.EpicRecord, error) {
	if err := s.feature.Require("epics"); err != nil {
		return nil, err
	}
	return s.store.ListEpics(sqlstore.EpicFilter{Status: status, ProjectID: projectID})
}
```

- [ ] **Step 6: Update service tests**

Update `sprint_test.go`: Remove `Goal` from inputs. Change expected default status from `"planning"` to `"active"`. Update transition tests to test `active` <-> `inactive`. Update `List` calls to pass two args: `svc.Sprint.List("", "")`.

Update `project_test.go`: Update `List` calls to `svc.Project.List("")`. Add tests for `Update` and status validation.

Update `epic_test.go`: Change expected default status from `"open"` to `"active"`. Update `List` calls to `svc.Epic.List("", "")`. Update `EpicCreateInput` and `EpicUpdateInput` to match new signatures.

- [ ] **Step 7: Run all service tests**

Run: `go test ./internal/service/... -count=1 -v`
Expected: ALL PASS

- [ ] **Step 8: Commit**

```bash
git add internal/service/sprint.go internal/service/project.go internal/service/epic.go
git add internal/service/sprint_test.go internal/service/project_test.go internal/service/epic_test.go
git commit -m "feat: update service layer — active/inactive statuses, project_id links, enriched inputs"
```

---

## Task 6: HTTP Endpoints — Projects

**Files:**
- Create: `internal/httpserver/projects.go`
- Modify: `internal/httpserver/server.go`

- [ ] **Step 1: Create projects.go with JSON helper and CRUD handlers**

```go
package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func projectJSON(p *sqlstore.ProjectRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":          p.ID,
		"name":        p.Name,
		"description": p.Description,
		"repo_path":   p.RepoPath,
		"status":      p.Status,
		"icon":        p.Icon,
		"created_at":  p.CreatedAt,
		"updated_at":  p.UpdatedAt,
	}
}

func projectsJSON(projects []sqlstore.ProjectRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, len(projects))
	for i := range projects {
		out[i] = projectJSON(&projects[i])
	}
	return out
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	projects, err := s.svc.Project.List(status)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if projects == nil {
		projects = []sqlstore.ProjectRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"projects": projectsJSON(projects)})
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := s.svc.Project.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projectJSON(project))
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		RepoPath    string `json:"repo_path"`
		Icon        string `json:"icon"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	project, err := s.svc.Project.Create(service.ProjectCreateInput{
		Name:        req.Name,
		Description: req.Description,
		RepoPath:    req.RepoPath,
		Icon:        req.Icon,
	})
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("project.created", map[string]interface{}{"project_id": project.ID})
	writeJSON(w, http.StatusCreated, projectJSON(project))
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req map[string]interface{}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	update := sqlstore.ProjectUpdate{}
	if v, ok := req["name"].(string); ok {
		update.Name = &v
	}
	if v, ok := req["description"].(string); ok {
		update.Description = &v
	}
	if v, ok := req["repo_path"].(string); ok {
		update.RepoPath = &v
	}
	if v, ok := req["status"].(string); ok {
		update.Status = &v
	}
	if v, ok := req["icon"].(string); ok {
		update.Icon = &v
	}

	if err := s.svc.Project.Update(id, update); err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	project, err := s.svc.Project.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("project.updated", map[string]interface{}{"project_id": id})
	writeJSON(w, http.StatusOK, projectJSON(project))
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Project.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("project.deleted", map[string]interface{}{"project_id": id})
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 2: Register routes in server.go**

Add inside the `r.Route("/api/v1", ...)` block:

```go
		// Projects
		r.Get("/projects", s.listProjects)
		r.Post("/projects", s.createProject)
		r.Get("/projects/{id}", s.getProject)
		r.Put("/projects/{id}", s.updateProject)
		r.Delete("/projects/{id}", s.deleteProject)
```

- [ ] **Step 3: Run all tests**

Run: `go test ./internal/... -count=1`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add internal/httpserver/projects.go internal/httpserver/server.go
git commit -m "feat: add REST endpoints for projects CRUD"
```

---

## Task 7: HTTP Endpoints — Sprints

**Files:**
- Create: `internal/httpserver/sprints.go`
- Modify: `internal/httpserver/server.go`

- [ ] **Step 1: Create sprints.go with JSON helper and CRUD + transition handlers**

```go
package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func sprintJSON(sp *sqlstore.SprintRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":            sp.ID,
		"name":          sp.Name,
		"status":        sp.Status,
		"approval_mode": sp.ApprovalMode,
		"cost_budget":   nullFloat(sp.CostBudget),
		"project_id":    nullStr(sp.ProjectID),
		"started_at":    nullTime(sp.StartedAt),
		"ended_at":      nullTime(sp.EndedAt),
		"created_at":    sp.CreatedAt,
		"updated_at":    sp.UpdatedAt,
	}
}

func sprintsJSON(sprints []sqlstore.SprintRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, len(sprints))
	for i := range sprints {
		out[i] = sprintJSON(&sprints[i])
	}
	return out
}

func (s *Server) listSprints(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	projectID := r.URL.Query().Get("project_id")
	sprints, err := s.svc.Sprint.List(status, projectID)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sprints == nil {
		sprints = []sqlstore.SprintRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"sprints": sprintsJSON(sprints)})
}

func (s *Server) getSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sprint, err := s.svc.Sprint.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sprintJSON(sprint))
}

func (s *Server) createSprint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string   `json:"name"`
		ApprovalMode string   `json:"approval_mode"`
		CostBudget   *float64 `json:"cost_budget"`
		ProjectID    string   `json:"project_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	sprint, err := s.svc.Sprint.Create(service.SprintCreateInput{
		Name:         req.Name,
		ApprovalMode: req.ApprovalMode,
		CostBudget:   req.CostBudget,
		ProjectID:    req.ProjectID,
	})
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("sprint.created", map[string]interface{}{"sprint_id": sprint.ID})
	writeJSON(w, http.StatusCreated, sprintJSON(sprint))
}

func (s *Server) updateSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req map[string]interface{}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	update := sqlstore.SprintUpdate{}
	if v, ok := req["name"].(string); ok {
		update.Name = &v
	}
	if v, ok := req["approval_mode"].(string); ok {
		update.ApprovalMode = &v
	}
	if v, ok := req["cost_budget"].(float64); ok {
		update.CostBudget = &v
	}
	if v, ok := req["project_id"].(string); ok {
		update.ProjectID = &v
	}

	if err := s.svc.Sprint.Update(id, update); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sprint, err := s.svc.Sprint.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("sprint.updated", map[string]interface{}{"sprint_id": id})
	writeJSON(w, http.StatusOK, sprintJSON(sprint))
}

func (s *Server) deleteSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Sprint.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("sprint.deleted", map[string]interface{}{"sprint_id": id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) transitionSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := s.svc.Sprint.Transition(id, req.Status); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	sprint, err := s.svc.Sprint.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("sprint.transitioned", map[string]interface{}{"sprint_id": id, "status": req.Status})
	writeJSON(w, http.StatusOK, sprintJSON(sprint))
}
```

- [ ] **Step 2: Add nullTime helper to tasks.go** (shared utility)

Add to `tasks.go` after `nullInt`:

```go
func nullTime(nt sql.NullTime) interface{} {
	if nt.Valid {
		return nt.Time
	}
	return nil
}
```

And add `"database/sql"` to its imports if not already there.

- [ ] **Step 3: Register routes in server.go**

```go
		// Sprints
		r.Get("/sprints", s.listSprints)
		r.Post("/sprints", s.createSprint)
		r.Get("/sprints/{id}", s.getSprint)
		r.Put("/sprints/{id}", s.updateSprint)
		r.Delete("/sprints/{id}", s.deleteSprint)
		r.Post("/sprints/{id}/transition", s.transitionSprint)
```

- [ ] **Step 4: Run all tests**

Run: `go test ./internal/... -count=1`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpserver/sprints.go internal/httpserver/server.go internal/httpserver/tasks.go
git commit -m "feat: add REST endpoints for sprints CRUD + transition"
```

---

## Task 8: HTTP Endpoints — Epics

**Files:**
- Create: `internal/httpserver/epics.go`
- Modify: `internal/httpserver/server.go`

- [ ] **Step 1: Create epics.go with JSON helper and CRUD handlers**

```go
package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func epicJSON(e *sqlstore.EpicRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":          e.ID,
		"name":        e.Name,
		"description": e.Description,
		"status":      e.Status,
		"priority":    nullInt(e.Priority),
		"project_id":  nullStr(e.ProjectID),
		"created_at":  e.CreatedAt,
		"updated_at":  e.UpdatedAt,
	}
}

func epicsJSON(epics []sqlstore.EpicRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, len(epics))
	for i := range epics {
		out[i] = epicJSON(&epics[i])
	}
	return out
}

func (s *Server) listEpics(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	projectID := r.URL.Query().Get("project_id")
	epics, err := s.svc.Epic.List(status, projectID)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if epics == nil {
		epics = []sqlstore.EpicRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"epics": epicsJSON(epics)})
}

func (s *Server) getEpic(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	epic, err := s.svc.Epic.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, epicJSON(epic))
}

func (s *Server) createEpic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Priority    *int64 `json:"priority"`
		ProjectID   string `json:"project_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	epic, err := s.svc.Epic.Create(service.EpicCreateInput{
		Name:        req.Name,
		Description: req.Description,
		Priority:    req.Priority,
		ProjectID:   req.ProjectID,
	})
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("epic.created", map[string]interface{}{"epic_id": epic.ID})
	writeJSON(w, http.StatusCreated, epicJSON(epic))
}

func (s *Server) updateEpic(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req map[string]interface{}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	update := service.EpicUpdateInput{}
	if v, ok := req["name"].(string); ok {
		update.Name = &v
	}
	if v, ok := req["description"].(string); ok {
		update.Description = &v
	}
	if v, ok := req["status"].(string); ok {
		update.Status = &v
	}
	if v, ok := req["priority"].(float64); ok {
		p := int64(v)
		update.Priority = &p
	}
	if v, ok := req["project_id"].(string); ok {
		update.ProjectID = &v
	}

	if err := s.svc.Epic.Update(id, update); err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	epic, err := s.svc.Epic.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("epic.updated", map[string]interface{}{"epic_id": id})
	writeJSON(w, http.StatusOK, epicJSON(epic))
}

func (s *Server) deleteEpic(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Epic.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("epic.deleted", map[string]interface{}{"epic_id": id})
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 2: Register routes in server.go**

```go
		// Epics
		r.Get("/epics", s.listEpics)
		r.Post("/epics", s.createEpic)
		r.Get("/epics/{id}", s.getEpic)
		r.Put("/epics/{id}", s.updateEpic)
		r.Delete("/epics/{id}", s.deleteEpic)
```

- [ ] **Step 3: Run all backend tests**

Run: `go test ./internal/... -count=1`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add internal/httpserver/epics.go internal/httpserver/server.go
git commit -m "feat: add REST endpoints for epics CRUD"
```

---

## Task 9: Frontend Types and API Client

**Files:**
- Modify: `apps/gui/src/lib/types.ts`
- Modify: `apps/gui/src/lib/api.ts`
- Modify: `apps/gui/src/lib/constants.ts`

- [ ] **Step 1: Add Sprint, Project, Epic types to types.ts**

```typescript
export type ContainerStatus = 'active' | 'inactive'

export interface Project {
  id: string
  name: string
  description: string
  repo_path: string
  status: ContainerStatus
  icon: string
  created_at: string
  updated_at: string
}

export interface Sprint {
  id: string
  name: string
  status: ContainerStatus
  approval_mode: string
  cost_budget: number | null
  project_id: string | null
  started_at: string | null
  ended_at: string | null
  created_at: string
  updated_at: string
}

export interface Epic {
  id: string
  name: string
  description: string
  status: ContainerStatus
  priority: number | null
  project_id: string | null
  created_at: string
  updated_at: string
}
```

- [ ] **Step 2: Add CRUD methods to api.ts**

Add to the `TorqueApiClient` class:

```typescript
  // -------------------------
  // Projects
  // -------------------------

  async listProjects(status?: string): Promise<{ projects: Project[] }> {
    const params: Record<string, string | number | boolean | undefined> = {}
    if (status) params['status'] = status
    return this.get<{ projects: Project[] }>('/projects', params)
  }

  async getProject(id: string): Promise<Project> {
    return this.get<Project>(`/projects/${id}`)
  }

  async createProject(data: Partial<Project>): Promise<Project> {
    return this.post<Project>('/projects', data)
  }

  async updateProject(id: string, data: Partial<Project>): Promise<Project> {
    return this.patch<Project>(`/projects/${id}`, data)
  }

  async deleteProject(id: string): Promise<void> {
    return this.delete<void>(`/projects/${id}`)
  }

  // -------------------------
  // Sprints
  // -------------------------

  async listSprints(params?: { status?: string; project_id?: string }): Promise<{ sprints: Sprint[] }> {
    return this.get<{ sprints: Sprint[] }>('/sprints', params)
  }

  async getSprint(id: string): Promise<Sprint> {
    return this.get<Sprint>(`/sprints/${id}`)
  }

  async createSprint(data: Partial<Sprint>): Promise<Sprint> {
    return this.post<Sprint>('/sprints', data)
  }

  async updateSprint(id: string, data: Partial<Sprint>): Promise<Sprint> {
    return this.patch<Sprint>(`/sprints/${id}`, data)
  }

  async deleteSprint(id: string): Promise<void> {
    return this.delete<void>(`/sprints/${id}`)
  }

  async transitionSprint(id: string, status: string): Promise<Sprint> {
    return this.post<Sprint>(`/sprints/${id}/transition`, { status })
  }

  // -------------------------
  // Epics
  // -------------------------

  async listEpics(params?: { status?: string; project_id?: string }): Promise<{ epics: Epic[] }> {
    return this.get<{ epics: Epic[] }>('/epics', params)
  }

  async getEpic(id: string): Promise<Epic> {
    return this.get<Epic>(`/epics/${id}`)
  }

  async createEpic(data: Partial<Epic>): Promise<Epic> {
    return this.post<Epic>('/epics', data)
  }

  async updateEpic(id: string, data: Partial<Epic>): Promise<Epic> {
    return this.patch<Epic>(`/epics/${id}`, data)
  }

  async deleteEpic(id: string): Promise<void> {
    return this.delete<void>(`/epics/${id}`)
  }
```

Note: The API client uses `PUT` for updates but the backend expects `PUT`. The existing `patch` helper sends `PATCH`. Add a `put` helper or change the update methods to use `put`:

```typescript
  private async put<T>(path: string, body: unknown): Promise<T> {
    const res = await fetch(this.url(path), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify(body),
    })
    return parseResponse<T>(res)
  }
```

Then change `updateProject`, `updateSprint`, `updateEpic` to use `this.put` instead of `this.patch`.

- [ ] **Step 3: Add container status constants to constants.ts**

```typescript
export const CONTAINER_STATUSES = ['active', 'inactive'] as const

export const CONTAINER_STATUS_COLORS: Record<string, { bg: string; text: string; border: string; dot: string }> = {
  active:   { bg: 'bg-emerald-500/10', text: 'text-emerald-200', border: 'border-emerald-500/40', dot: 'bg-emerald-300' },
  inactive: { bg: 'bg-zinc-500/10',    text: 'text-zinc-400',    border: 'border-zinc-500/40',    dot: 'bg-zinc-500'    },
}
```

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/lib/types.ts apps/gui/src/lib/api.ts apps/gui/src/lib/constants.ts
git commit -m "feat: add frontend types, API methods, and constants for projects/sprints/epics"
```

---

## Task 10: Shared GUI Components

**Files:**
- Create: `apps/gui/src/components/domain/summary-cards.tsx`
- Create: `apps/gui/src/components/domain/page-header.tsx`
- Create: `apps/gui/src/components/domain/detail-header.tsx`
- Create: `apps/gui/src/components/domain/copyable-id.tsx`
- Create: `apps/gui/src/components/domain/row-actions.tsx`
- Create: `apps/gui/src/components/domain/progress-bar.tsx`

- [ ] **Step 1: Create SummaryCards component**

`apps/gui/src/components/domain/summary-cards.tsx`:

```tsx
interface SummaryCard {
  label: string
  value: number | string
  subtitle?: string
  /** Optional accent color for the bottom bar */
  accentColor?: string
}

interface SummaryCardsProps {
  cards: SummaryCard[]
}

export function SummaryCards({ cards }: SummaryCardsProps) {
  return (
    <div className="grid grid-cols-4 gap-3 px-4 py-3">
      {cards.map((card) => (
        <div
          key={card.label}
          className="rounded-md border border-zinc-800/80 bg-zinc-950 p-3"
        >
          <div className="text-[10px] uppercase tracking-[.16em] text-zinc-500">
            {card.label}
          </div>
          <div className="mt-1 flex items-baseline gap-2">
            <span className="font-mono text-2xl font-semibold text-zinc-100">
              {card.value}
            </span>
            {card.subtitle && (
              <span className="text-[10px] text-zinc-500">{card.subtitle}</span>
            )}
          </div>
          {card.accentColor && (
            <div
              className="mt-2 h-0.5 w-full rounded-full"
              style={{ backgroundColor: card.accentColor }}
            />
          )}
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 2: Create PageHeader component**

`apps/gui/src/components/domain/page-header.tsx`:

```tsx
interface PageHeaderProps {
  title: string
  children?: React.ReactNode
}

export function PageHeader({ title, children }: PageHeaderProps) {
  return (
    <div className="flex items-center justify-between border-b border-zinc-800/80 bg-zinc-950 px-4 py-2.5">
      <h1 className="text-sm font-semibold uppercase tracking-[.14em] text-zinc-300">
        {title}
      </h1>
      {children && <div className="flex items-center gap-2">{children}</div>}
    </div>
  )
}
```

- [ ] **Step 3: Create DetailHeader component**

`apps/gui/src/components/domain/detail-header.tsx`:

```tsx
import { ArrowLeft } from 'lucide-react'
import { Link } from 'react-router-dom'
import { StatusBadge } from './status-badge'

interface DetailHeaderProps {
  title: string
  backTo: string
  backLabel: string
  id: string
  status?: string
  children?: React.ReactNode
}

export function DetailHeader({ title, backTo, backLabel, id, status, children }: DetailHeaderProps) {
  return (
    <div className="border-b border-border bg-card px-6 py-4">
      <div className="flex items-center gap-2 mb-3">
        <Link
          to={backTo}
          className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          {backLabel}
        </Link>
        <span className="text-muted-foreground/40 text-xs">/</span>
        <span className="text-xs text-muted-foreground font-mono">{id}</span>
      </div>

      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-2 min-w-0">
          <h1 className="text-xl font-semibold text-foreground leading-tight">{title}</h1>
          <div className="flex items-center gap-2 flex-wrap">
            {status && <StatusBadge status={status} />}
            {children}
          </div>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Create CopyableId component**

`apps/gui/src/components/domain/copyable-id.tsx`:

```tsx
import { useState } from 'react'
import { Check, Copy } from 'lucide-react'

interface CopyableIdProps {
  id: string
}

export function CopyableId({ id }: CopyableIdProps) {
  const [copied, setCopied] = useState(false)

  function handleCopy() {
    navigator.clipboard.writeText(id)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <button
      type="button"
      onClick={handleCopy}
      className="inline-flex items-center gap-1 text-[10px] font-mono text-zinc-500 hover:text-zinc-300 transition-colors"
      title="Copy ID"
    >
      {id}
      {copied ? (
        <Check className="h-2.5 w-2.5 text-emerald-400" />
      ) : (
        <Copy className="h-2.5 w-2.5" />
      )}
    </button>
  )
}
```

- [ ] **Step 5: Create RowActions dropdown component**

`apps/gui/src/components/domain/row-actions.tsx`:

```tsx
import { MoreHorizontal } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

export interface RowAction {
  label: string
  onClick: () => void
  variant?: 'default' | 'destructive'
}

interface RowActionsProps {
  actions: RowAction[]
}

export function RowActions({ actions }: RowActionsProps) {
  const normal = actions.filter((a) => a.variant !== 'destructive')
  const destructive = actions.filter((a) => a.variant === 'destructive')

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" className="h-7 w-7 p-0">
          <MoreHorizontal className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {normal.map((action) => (
          <DropdownMenuItem key={action.label} onClick={action.onClick}>
            {action.label}
          </DropdownMenuItem>
        ))}
        {destructive.length > 0 && <DropdownMenuSeparator />}
        {destructive.map((action) => (
          <DropdownMenuItem
            key={action.label}
            onClick={action.onClick}
            className="text-red-400 focus:text-red-300"
          >
            {action.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
```

- [ ] **Step 6: Create ProgressBar component**

`apps/gui/src/components/domain/progress-bar.tsx`:

```tsx
interface ProgressBarProps {
  value: number // 0-100
  className?: string
}

export function ProgressBar({ value, className = '' }: ProgressBarProps) {
  const clamped = Math.max(0, Math.min(100, value))
  return (
    <div className={`h-1 w-full rounded-full bg-zinc-800 ${className}`}>
      <div
        className="h-full rounded-full bg-emerald-500 transition-all duration-300"
        style={{ width: `${clamped}%` }}
      />
    </div>
  )
}
```

- [ ] **Step 7: Commit**

```bash
git add apps/gui/src/components/domain/summary-cards.tsx \
       apps/gui/src/components/domain/page-header.tsx \
       apps/gui/src/components/domain/detail-header.tsx \
       apps/gui/src/components/domain/copyable-id.tsx \
       apps/gui/src/components/domain/row-actions.tsx \
       apps/gui/src/components/domain/progress-bar.tsx
git commit -m "feat: add shared GUI components — summary cards, page/detail headers, copyable ID, row actions, progress bar"
```

---

## Task 11: Projects Home Page

**Files:**
- Create: `apps/gui/src/pages/ProjectsPage.tsx`
- Modify: `apps/gui/src/App.tsx`

- [ ] **Step 1: Create ProjectsPage**

`apps/gui/src/pages/ProjectsPage.tsx`:

```tsx
import { useState, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { FilterBar } from '@/components/domain/filter-bar'
import { StatusBadge } from '@/components/domain/status-badge'
import { CopyableId } from '@/components/domain/copyable-id'
import { RowActions } from '@/components/domain/row-actions'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import type { Project, ContainerStatus } from '@/lib/types'

const SSE_EVENTS = ['project.created', 'project.updated', 'project.deleted']

type SortKey = 'name' | 'status' | 'updated_at'
type SortDir = 'asc' | 'desc'

export default function ProjectsPage() {
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [projects, setProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [statusFilter, setStatusFilter] = useState<ContainerStatus | ''>('')
  const [sortKey, setSortKey] = useState<SortKey>('name')
  const [sortDir, setSortDir] = useState<SortDir>('asc')

  const fetchProjects = useCallback(async () => {
    try {
      const result = await api.listProjects(statusFilter || undefined)
      setProjects(result.projects)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load projects')
    } finally {
      setLoading(false)
    }
  }, [api, statusFilter])

  useEffect(() => {
    setLoading(true)
    fetchProjects()
  }, [fetchProjects])

  useEffect(() => {
    if (lastEvent) fetchProjects()
  }, [lastEvent, fetchProjects])

  function handleSort(key: SortKey) {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  const sorted = [...projects].sort((a, b) => {
    const av = a[sortKey] ?? ''
    const bv = b[sortKey] ?? ''
    const cmp = av < bv ? -1 : av > bv ? 1 : 0
    return sortDir === 'asc' ? cmp : -cmp
  })

  const activeCount = projects.filter((p) => p.status === 'active').length
  const inactiveCount = projects.filter((p) => p.status === 'inactive').length

  const cards = [
    { label: 'Total Projects', value: projects.length },
    { label: 'Active', value: activeCount, accentColor: '#34d399' },
    { label: 'Inactive', value: inactiveCount },
    { label: 'Catalog', value: projects.length, subtitle: 'all' },
  ]

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Projects">
        <Button size="sm" className="text-xs h-7" variant="outline">
          New
        </Button>
      </PageHeader>
      <SummaryCards cards={cards} />
      <div className="flex items-center gap-2 border-b border-zinc-800/80 px-4 py-2 text-xs">
        <span className="text-[10px] uppercase tracking-[.16em] text-zinc-500">Status:</span>
        {(['', 'active', 'inactive'] as const).map((s) => (
          <button
            key={s || 'all'}
            type="button"
            onClick={() => setStatusFilter(s as ContainerStatus | '')}
            className={`rounded border px-2 py-0.5 text-[10px] uppercase tracking-wider transition-all ${
              statusFilter === s
                ? 'border-zinc-600 bg-zinc-800 text-zinc-200'
                : 'border-zinc-800 bg-zinc-900/50 text-zinc-600 hover:text-zinc-400'
            }`}
          >
            {s || 'All'}
          </button>
        ))}
      </div>

      <div className="flex-1 overflow-auto">
        {loading ? (
          <div className="flex flex-col gap-2 p-4">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full rounded-md" />
            ))}
          </div>
        ) : error ? (
          <EmptyState variant="error" description={error} action={{ label: 'Retry', onClick: fetchProjects }} />
        ) : sorted.length === 0 ? (
          <EmptyState variant="no-tasks" title="No projects" description="Create a project to get started." />
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full">
              <thead className="text-[10px] uppercase tracking-[.28em] text-zinc-500">
                <tr className="border-b border-zinc-800/80">
                  <th className="px-4 py-2 text-left font-medium">
                    <button type="button" className="hover:text-zinc-300" onClick={() => handleSort('name')}>
                      Project {sortKey === 'name' ? (sortDir === 'asc' ? '↑' : '↓') : '⇕'}
                    </button>
                  </th>
                  <th className="px-2 py-2 text-left font-medium">
                    <button type="button" className="hover:text-zinc-300" onClick={() => handleSort('status')}>
                      Status {sortKey === 'status' ? (sortDir === 'asc' ? '↑' : '↓') : '⇕'}
                    </button>
                  </th>
                  <th className="px-2 py-2 text-left font-medium">Repo</th>
                  <th className="px-2 py-2 text-right font-medium">
                    <button type="button" className="hover:text-zinc-300" onClick={() => handleSort('updated_at')}>
                      Updated {sortKey === 'updated_at' ? (sortDir === 'asc' ? '↑' : '↓') : '⇕'}
                    </button>
                  </th>
                  <th className="w-10 px-2 py-2" />
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800/60 text-[13px] leading-4">
                {sorted.map((project) => (
                  <tr
                    key={project.id}
                    className="hover:bg-zinc-900/40 cursor-pointer"
                    onClick={() => navigate(`/projects/${project.id}`)}
                  >
                    <td className="px-4 py-2.5">
                      <div className="flex items-center gap-2">
                        <div className="flex h-7 w-7 items-center justify-center rounded border border-zinc-800/80 bg-zinc-900 text-[10px] font-bold uppercase text-zinc-400">
                          {project.icon || project.name.slice(0, 2)}
                        </div>
                        <div>
                          <div className="font-medium text-zinc-100">{project.name}</div>
                          <CopyableId id={project.id} />
                        </div>
                      </div>
                    </td>
                    <td className="px-2 py-2.5">
                      <StatusBadge status={project.status} />
                    </td>
                    <td className="px-2 py-2.5 text-xs font-mono text-zinc-500 max-w-48 truncate">
                      {project.repo_path || '—'}
                    </td>
                    <td className="px-2 py-2.5 text-right text-xs text-zinc-500">
                      {new Date(project.updated_at).toLocaleDateString()}
                    </td>
                    <td className="px-2 py-2.5" onClick={(e) => e.stopPropagation()}>
                      <RowActions
                        actions={[
                          {
                            label: project.status === 'active' ? 'Deactivate' : 'Activate',
                            onClick: () => api.updateProject(project.id, {
                              status: project.status === 'active' ? 'inactive' : 'active',
                            }).then(fetchProjects),
                          },
                          { label: 'Delete', variant: 'destructive', onClick: () => api.deleteProject(project.id).then(fetchProjects) },
                        ]}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Add route and nav item to App.tsx**

Add import:
```tsx
import ProjectsPage from '@/pages/ProjectsPage'
import ProjectDetailPage from '@/pages/ProjectDetailPage'
```

Add nav icon import:
```tsx
import { LayoutList, Play, BarChart3, Settings, Cog, FolderOpen, Layers, Milestone } from 'lucide-react'
```

Add nav items after Board:
```tsx
        <NavItem to="/projects" label="Projects">
          <FolderOpen className="h-4 w-4" />
        </NavItem>
        <NavItem to="/sprints" label="Sprints">
          <Milestone className="h-4 w-4" />
        </NavItem>
        <NavItem to="/epics" label="Epics">
          <Layers className="h-4 w-4" />
        </NavItem>
```

Add routes:
```tsx
          <Route path="/projects" element={<ProjectsPage />} />
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
          <Route path="/sprints" element={<SprintsPage />} />
          <Route path="/sprints/:id" element={<SprintDetailPage />} />
          <Route path="/epics" element={<EpicsPage />} />
          <Route path="/epics/:id" element={<EpicDetailPage />} />
```

Note: Import the pages as they are created in subsequent tasks. Use placeholder empty components initially if needed, or add the imports in the task where each page is created.

- [ ] **Step 3: Verify the app compiles**

Run: `cd apps/gui && npx tsc --noEmit`

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/pages/ProjectsPage.tsx apps/gui/src/App.tsx
git commit -m "feat: add Projects home page with summary cards, table, filtering"
```

---

## Task 12: Project Detail Page

**Files:**
- Create: `apps/gui/src/pages/ProjectDetailPage.tsx`

- [ ] **Step 1: Create ProjectDetailPage**

`apps/gui/src/pages/ProjectDetailPage.tsx`:

```tsx
import { useState, useEffect } from 'react'
import { useParams } from 'react-router-dom'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { DetailHeader } from '@/components/domain/detail-header'
import { ProgressBar } from '@/components/domain/progress-bar'
import { TaskTable } from '@/components/domain/task-table'
import { FilterBar } from '@/components/domain/filter-bar'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { DEFAULT_ACTIVE_STATUSES, MODE_PRESETS } from '@/lib/constants'
import type { Project, Task, Sprint, TaskStatus } from '@/lib/types'

type ModePreset = keyof typeof MODE_PRESETS | 'all'

export default function ProjectDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()

  const [project, setProject] = useState<Project | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [activeTab, setActiveTab] = useState('sprints')

  // Sprints tab
  const [sprints, setSprints] = useState<Sprint[] | null>(null)

  // Tasks tab
  const [tasks, setTasks] = useState<Task[] | null>(null)
  const [activeStatuses, setActiveStatuses] = useState<TaskStatus[]>(DEFAULT_ACTIVE_STATUSES)
  const [mode, setMode] = useState<ModePreset>('all')

  useEffect(() => {
    if (!id) return
    setLoading(true)
    api.getProject(id)
      .then((p) => { setProject(p); setError(null) })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false))
  }, [api, id])

  useEffect(() => {
    if (!id || !project) return
    if (activeTab === 'sprints' && sprints === null) {
      api.listSprints({ project_id: id }).then((r) => setSprints(r.sprints)).catch(() => setSprints([]))
    }
    if (activeTab === 'tasks' && tasks === null) {
      api.listTasks({ project_id: id, status: activeStatuses })
        .then((r) => setTasks(r.tasks))
        .catch(() => setTasks([]))
    }
  }, [activeTab, id, project, sprints, tasks, api, activeStatuses])

  function handleStatusToggle(status: TaskStatus) {
    setActiveStatuses((prev) =>
      prev.includes(status) ? prev.filter((s) => s !== status) : [...prev, status]
    )
    setTasks(null) // refetch
  }

  function handleModeChange(newMode: ModePreset) {
    setMode(newMode)
    if (newMode === 'all') {
      setActiveStatuses(DEFAULT_ACTIVE_STATUSES)
    } else {
      setActiveStatuses(MODE_PRESETS[newMode])
    }
    setTasks(null)
  }

  async function handleTransition(taskId: string, status: TaskStatus) {
    try {
      await api.transitionTask(taskId, status)
      setTasks(null)
    } catch { /* no-op */ }
  }

  if (loading) {
    return (
      <div className="p-6 flex flex-col gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-5 w-96" />
        <Skeleton className="h-40 w-full rounded-lg" />
      </div>
    )
  }

  if (error || !project) {
    return (
      <div className="p-6">
        <EmptyState variant="error" description={error ?? 'Project not found.'} />
      </div>
    )
  }

  const tasksDone = tasks?.filter((t) => t.status === 'done').length ?? 0
  const tasksTotal = tasks?.length ?? 0
  const progress = tasksTotal > 0 ? Math.round((tasksDone / tasksTotal) * 100) : 0

  return (
    <div className="flex h-full flex-col">
      <DetailHeader
        title={project.name}
        backTo="/projects"
        backLabel="Projects"
        id={project.id}
        status={project.status}
      />

      {/* Progress bar */}
      <div className="px-6 py-3 border-b border-border">
        <div className="flex items-center justify-between text-xs text-muted-foreground mb-1">
          <span>Progress</span>
          <span>{tasksDone}/{tasksTotal} ({progress}%)</span>
        </div>
        <ProgressBar value={progress} />
      </div>

      {/* Project metadata */}
      <div className="px-6 py-3 border-b border-border">
        <dl className="flex gap-6 text-xs">
          {project.repo_path && (
            <div>
              <dt className="text-muted-foreground mb-0.5">Root</dt>
              <dd className="font-mono text-foreground">{project.repo_path}</dd>
            </div>
          )}
          {project.description && (
            <div>
              <dt className="text-muted-foreground mb-0.5">Description</dt>
              <dd className="text-foreground">{project.description}</dd>
            </div>
          )}
        </dl>
      </div>

      {/* Tabs */}
      <div className="flex-1 overflow-auto p-6">
        <Tabs value={activeTab} onValueChange={setActiveTab}>
          <TabsList className="mb-4">
            <TabsTrigger value="sprints">Sprints</TabsTrigger>
            <TabsTrigger value="tasks">Tasks</TabsTrigger>
          </TabsList>

          <TabsContent value="sprints">
            {sprints === null ? (
              <div className="flex flex-col gap-2">
                {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-10 w-full rounded-md" />)}
              </div>
            ) : sprints.length === 0 ? (
              <EmptyState variant="no-results" title="No sprints" description="No sprints linked to this project." />
            ) : (
              <table className="min-w-full">
                <thead className="text-[10px] uppercase tracking-[.28em] text-zinc-500">
                  <tr className="border-b border-zinc-800/80">
                    <th className="px-3 py-2 text-left font-medium">Sprint</th>
                    <th className="px-2 py-2 text-left font-medium">Status</th>
                    <th className="px-2 py-2 text-right font-medium">Updated</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-zinc-800/60 text-[13px]">
                  {sprints.map((sprint) => (
                    <tr key={sprint.id} className="hover:bg-zinc-900/40">
                      <td className="px-3 py-2 text-zinc-100">{sprint.name}</td>
                      <td className="px-2 py-2">
                        <StatusBadge status={sprint.status} />
                      </td>
                      <td className="px-2 py-2 text-right text-xs text-zinc-500">
                        {new Date(sprint.updated_at).toLocaleDateString()}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </TabsContent>

          <TabsContent value="tasks">
            <FilterBar
              activeStatuses={activeStatuses}
              onStatusToggle={handleStatusToggle}
              mode={mode}
              onModeChange={handleModeChange}
            />
            {tasks === null ? (
              <div className="flex flex-col gap-2 mt-4">
                {Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} className="h-10 w-full rounded-md" />)}
              </div>
            ) : (
              <div className="mt-2">
                <TaskTable tasks={tasks} onTransition={handleTransition} />
              </div>
            )}
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}
```

Note: Import `StatusBadge` at top of file.

- [ ] **Step 2: Verify the app compiles**

Run: `cd apps/gui && npx tsc --noEmit`

- [ ] **Step 3: Commit**

```bash
git add apps/gui/src/pages/ProjectDetailPage.tsx
git commit -m "feat: add Project detail page with sprints/tasks tabs and progress bar"
```

---

## Task 13: Sprints Home Page + Detail Page

**Files:**
- Create: `apps/gui/src/pages/SprintsPage.tsx`
- Create: `apps/gui/src/pages/SprintDetailPage.tsx`

These follow the exact same patterns as Tasks 11 and 12 but for sprints. The SprintsPage shows summary cards (Total, Active, Inactive), a status filter bar, and a sortable table with columns: Sprint (name + copyable ID), Status, Project (name or "—"), Progress (task counts), Updated, Actions.

The SprintDetailPage shows the sprint header with status badge, a progress bar of task completion, and a Tasks tab with FilterBar + TaskTable filtered by `sprint_id`.

- [ ] **Step 1: Create SprintsPage** following the same structure as ProjectsPage (Task 11) but fetching from `api.listSprints()`. Table columns: Sprint name + CopyableId, Status badge, Project name (fetched via a separate `api.listProjects()` call to build a lookup map), Updated date, RowActions (Activate/Deactivate, Delete).

- [ ] **Step 2: Create SprintDetailPage** following the same structure as ProjectDetailPage (Task 12) but with a single Tasks tab. Show sprint metadata: name, status, approval_mode, cost_budget (if set), project name (if linked). FilterBar + TaskTable filtered by `sprint_id`.

- [ ] **Step 3: Verify the app compiles**

Run: `cd apps/gui && npx tsc --noEmit`

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/pages/SprintsPage.tsx apps/gui/src/pages/SprintDetailPage.tsx
git commit -m "feat: add Sprints home and detail pages"
```

---

## Task 14: Epics Home Page + Detail Page

**Files:**
- Create: `apps/gui/src/pages/EpicsPage.tsx`
- Create: `apps/gui/src/pages/EpicDetailPage.tsx`

Same patterns as Tasks 11-12 but for epics. The EpicsPage shows summary cards (Total, Active, Inactive), a status filter bar, and a sortable table with columns: Epic (name + copyable ID), Status, Priority (PriorityBadge), Project (name or "—"), Updated, Actions.

The EpicDetailPage shows the epic header with status + priority badges, description, linked sprints (if project_id matches), and a Tasks tab with FilterBar + TaskTable filtered by `epic_id`.

- [ ] **Step 1: Create EpicsPage** following ProjectsPage pattern. Include priority badge column. Filter by status.

- [ ] **Step 2: Create EpicDetailPage** following ProjectDetailPage pattern. Show epic metadata: description, priority, project link. Tasks tab with FilterBar + TaskTable filtered by `epic_id`.

- [ ] **Step 3: Verify the app compiles**

Run: `cd apps/gui && npx tsc --noEmit`

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/pages/EpicsPage.tsx apps/gui/src/pages/EpicDetailPage.tsx
git commit -m "feat: add Epics home and detail pages"
```

---

## Task 15: Polish Tasks Board Page — Add Summary Cards + Context

**Files:**
- Modify: `apps/gui/src/pages/BoardPage.tsx`

- [ ] **Step 1: Add summary cards to BoardPage**

At the top of the BoardPage, before the FilterBar, add:

```tsx
import { SummaryCards } from '@/components/domain/summary-cards'
import { PageHeader } from '@/components/domain/page-header'
```

Add summary card computation after tasks load:

```tsx
  const openCount = tasks.filter((t) => ['backlog', 'todo', 'queued'].includes(t.status)).length
  const doingCount = tasks.filter((t) => t.status === 'doing').length
  const reviewCount = tasks.filter((t) => t.status === 'review').length
  const blockedCount = tasks.filter((t) => t.status === 'blocked').length

  const cards = [
    { label: 'Open Tasks', value: openCount },
    { label: 'In Progress', value: doingCount, accentColor: '#60a5fa' },
    { label: 'In Review', value: reviewCount, accentColor: '#a78bfa' },
    { label: 'Blocked', value: blockedCount, accentColor: '#f87171' },
  ]
```

Wrap the return with PageHeader + SummaryCards before FilterBar:

```tsx
  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Tasks" />
      {!loading && !error && <SummaryCards cards={cards} />}
      <FilterBar ... />
      ...
    </div>
  )
```

- [ ] **Step 2: Verify the app compiles**

Run: `cd apps/gui && npx tsc --noEmit`

- [ ] **Step 3: Commit**

```bash
git add apps/gui/src/pages/BoardPage.tsx
git commit -m "feat: add summary cards and page header to Tasks board"
```

---

## Task 16: Wire Up App Routes and Nav

**Files:**
- Modify: `apps/gui/src/App.tsx`

- [ ] **Step 1: Add all imports and routes**

Ensure all page imports are present and all routes + nav items are registered. This task consolidates anything from Tasks 11-14 that was deferred.

Add imports for all new pages:
```tsx
import ProjectsPage from '@/pages/ProjectsPage'
import ProjectDetailPage from '@/pages/ProjectDetailPage'
import SprintsPage from '@/pages/SprintsPage'
import SprintDetailPage from '@/pages/SprintDetailPage'
import EpicsPage from '@/pages/EpicsPage'
import EpicDetailPage from '@/pages/EpicDetailPage'
```

Add nav items (after Board, before Runs):
```tsx
        <NavItem to="/projects" label="Projects">
          <FolderOpen className="h-4 w-4" />
        </NavItem>
        <NavItem to="/sprints" label="Sprints">
          <Milestone className="h-4 w-4" />
        </NavItem>
        <NavItem to="/epics" label="Epics">
          <Layers className="h-4 w-4" />
        </NavItem>
```

Add routes:
```tsx
          <Route path="/projects" element={<ProjectsPage />} />
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
          <Route path="/sprints" element={<SprintsPage />} />
          <Route path="/sprints/:id" element={<SprintDetailPage />} />
          <Route path="/epics" element={<EpicsPage />} />
          <Route path="/epics/:id" element={<EpicDetailPage />} />
```

Add lucide imports:
```tsx
import { LayoutList, Play, BarChart3, Settings, Cog, FolderOpen, Layers, Milestone } from 'lucide-react'
```

- [ ] **Step 2: Build the full app**

Run: `cd apps/gui && npm run build`
Expected: Build succeeds with no errors

- [ ] **Step 3: Commit**

```bash
git add apps/gui/src/App.tsx
git commit -m "feat: wire up routes and nav for projects, sprints, epics pages"
```

---

## Task 17: Full Backend Test Run

- [ ] **Step 1: Run all Go tests**

Run: `cd /Users/chrispian/Projects-apps/torque && go test ./... -count=1`
Expected: ALL PASS

- [ ] **Step 2: Build the daemon**

Run: `make build` (or `go build ./cmd/torqued/`)
Expected: Build succeeds

- [ ] **Step 3: Build the CLI**

Run: `go build ./cmd/torque/`
Expected: Build succeeds

- [ ] **Step 4: Fix any failures, then commit**

```bash
git commit -m "chore: verify full test suite and builds pass"
```

---
