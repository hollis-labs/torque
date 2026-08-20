package service

import (
	"database/sql"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// IssueCreateInput is the minimal capture surface for a project-scoped issue.
type IssueCreateInput struct {
	Title     string
	Body      string
	ProjectID string
}

// IssueUpdateInput is the writable issue field set. Nil means unchanged.
type IssueUpdateInput struct {
	Title     *string
	Body      *string
	ProjectID *string
}

// IssueService wraps TaskService with issue-specific invariants. Issues are
// stored as kind=issue tasks with status=backlog and manual=true.
type IssueService struct {
	tasks   *TaskService
	feature *FeatureService
	store   *sqlstore.Store
}

// Create captures a new issue with the minimum required fields.
func (s *IssueService) Create(input IssueCreateInput) (*sqlstore.TaskRecord, error) {
	if input.Title == "" {
		return nil, &ValidationError{Field: "title", Message: "title is required"}
	}
	if input.Body == "" {
		return nil, &ValidationError{Field: "body", Message: "body is required"}
	}
	if input.ProjectID == "" {
		return nil, &ValidationError{Field: "project_id", Message: "project_id is required"}
	}
	if err := s.requireProject(input.ProjectID); err != nil {
		return nil, err
	}

	return s.tasks.Create(TaskCreateInput{
		Title:       input.Title,
		Description: input.Body,
		Status:      "backlog",
		ProjectID:   input.ProjectID,
		Kind:        "issue",
		Manual:      true,
	})
}

// Get returns an issue by task ID and rejects non-issue task rows.
func (s *IssueService) Get(id string) (*sqlstore.TaskRecord, error) {
	task, err := s.tasks.Get(id)
	if err != nil {
		return nil, err
	}
	if task.Kind != "issue" {
		return nil, &ValidationError{Field: "kind", Message: "task " + id + " is not an issue"}
	}
	return task, nil
}

// IssueListInput is the merged list/search filter for issues (ADR-0004 §3:
// "Search folds into list where it's the same query" — Issue's `_search`
// tool ran the identical query `_list`'s `search` param already ran, same
// redundancy pattern as Task). Query is optional: empty means a pure
// filtered list; non-empty adds a substring match over id/title/description
// (sqlstore.TaskFilter.Search semantics), exactly what the old dedicated
// Search method did.
type IssueListInput struct {
	ProjectID string
	Status    string
	Query     string

	// Limit is always pushed to the DB layer (sqlstore.TaskFilter.Limit) —
	// fixing the pre-merge bug where List never set Limit and the MCP
	// adapter fetched every row before truncating in Go. Limit<=0 means
	// "no limit" (sqlstore.ListTasks only applies LIMIT when > 0),
	// preserving the HTTP layer's existing unbounded-list behavior.
	Limit int

	// Sort + cursor pagination (PRIM-001/PRIM-002). Callers that don't pass
	// these get sqlstore.ListTasks' hardcoded `priority ASC, created_at ASC`
	// default order, matching pre-merge List/Search behavior.
	SortBy         string
	SortDir        string
	AfterSortValue string
	AfterID        string
}

// List returns issue rows honoring the project/status filters and optional
// substring search, with PRIM-001/PRIM-002 cursor pagination and sort —
// merged replacement for the former List(projectID)/Search(query,
// projectID, limit) pair (ADR-0004 §3; no compat guarantee has been made to
// external callers yet per the ADR's Consequences section, so the redundant
// pair is removed rather than kept alongside this).
func (s *IssueService) List(input IssueListInput) ([]sqlstore.TaskRecord, error) {
	if input.ProjectID != "" {
		if err := s.requireProject(input.ProjectID); err != nil {
			return nil, err
		}
	}
	return s.tasks.List(sqlstore.TaskFilter{
		Kind:           "issue",
		ProjectID:      input.ProjectID,
		Status:         input.Status,
		Search:         input.Query,
		Limit:          input.Limit,
		SortBy:         input.SortBy,
		SortDir:        input.SortDir,
		AfterSortValue: input.AfterSortValue,
		AfterID:        input.AfterID,
	})
}

// Update applies a minimal issue edit and keeps the row kind-scoped.
func (s *IssueService) Update(id string, input IssueUpdateInput) error {
	if _, err := s.Get(id); err != nil {
		return err
	}
	update := sqlstore.TaskUpdate{}
	if input.Title != nil {
		if *input.Title == "" {
			return &ValidationError{Field: "title", Message: "title is required"}
		}
		update.Title = input.Title
	}
	if input.Body != nil {
		if *input.Body == "" {
			return &ValidationError{Field: "body", Message: "body is required"}
		}
		update.Description = input.Body
	}
	if input.ProjectID != nil {
		if *input.ProjectID == "" {
			return &ValidationError{Field: "project_id", Message: "project_id is required"}
		}
		if err := s.requireProject(*input.ProjectID); err != nil {
			return err
		}
		update.ProjectID = &sql.NullString{String: *input.ProjectID, Valid: true}
	}
	return s.tasks.Update(id, TaskUpdateInput{TaskUpdate: update})
}

// BulkUpdate applies the same partial update to many issues (PRIM-003,
// mirroring TaskService.BulkUpdate in task_bulk.go). Per-item semantics are
// identical to Update — including its kind=issue scoping via Get, so an id
// that names a non-issue task row fails that item with a domain error
// rather than silently editing it — and a failure on one id does not stop
// the rest.
func (s *IssueService) BulkUpdate(ids []string, input IssueUpdateInput) ([]string, []BulkItemError) {
	return RunBulk(ids, func(id string) error {
		return s.Update(id, input)
	})
}

func (s *IssueService) requireProject(projectID string) error {
	if s.feature != nil {
		if err := s.feature.Require("projects"); err != nil {
			return err
		}
	}
	if _, err := s.store.GetProject(projectID); err != nil {
		return &ValidationError{Field: "project_id", Message: "project not found: " + projectID}
	}
	return nil
}
