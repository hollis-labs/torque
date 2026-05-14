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

// List returns issue rows, optionally narrowed to one project.
func (s *IssueService) List(projectID string) ([]sqlstore.TaskRecord, error) {
	if projectID != "" {
		if err := s.requireProject(projectID); err != nil {
			return nil, err
		}
	}
	return s.tasks.List(sqlstore.TaskFilter{Kind: "issue", ProjectID: projectID})
}

// Search returns issue rows whose ID, title, or body match query.
func (s *IssueService) Search(query, projectID string, limit int) ([]sqlstore.TaskRecord, error) {
	if query == "" {
		return nil, &ValidationError{Field: "query", Message: "query is required"}
	}
	if projectID != "" {
		if err := s.requireProject(projectID); err != nil {
			return nil, err
		}
	}
	return s.tasks.List(sqlstore.TaskFilter{
		Kind:      "issue",
		ProjectID: projectID,
		Search:    query,
		Limit:     limit,
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
