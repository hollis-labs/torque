package service

import (
	"database/sql"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// validTransitions defines the allowed FSM transitions for task status.
var validTransitions = map[string][]string{
	"todo":     {"doing", "blocked", "paused", "archived"},
	"doing":    {"review", "done", "blocked", "paused", "todo", "archived"},
	"review":   {"done", "doing", "blocked", "paused", "archived"},
	"blocked":  {"todo", "archived"},
	"paused":   {"todo", "archived"},
	"done":     {"archived"},
}

// TaskCreateInput holds user-facing fields for creating a task.
type TaskCreateInput struct {
	Title             string
	Description       string
	Priority          int
	Tags              []string
	Manual            bool
	Executor          string
	AgentProfile      string
	WorkingDir        string
	Tools             []string
	SystemPrompt      string
	Files             []string
	CostBudget        *float64
	MaxRetries        *int
	OnDone            string
	OnFail            string
	OnReview          string
	OnDoneMerge       string
	DeliverablePreset string
	DependsOn         []string
	SprintID          string
	ProjectID         string
	EpicID            string
}

// TaskService provides business logic for tasks.
type TaskService struct {
	store   *sqlstore.Store
	feature *FeatureService
	tags    *TagService
}

// Create validates and creates a new task.
func (s *TaskService) Create(input TaskCreateInput) (*sqlstore.TaskRecord, error) {
	if input.Title == "" {
		return nil, &ValidationError{Field: "title", Message: "title is required"}
	}

	priority := input.Priority
	if priority == 0 {
		priority = 2
	}

	// Validate sprint association
	if input.SprintID != "" {
		if s.feature != nil && s.feature.IsEnabled("sprints") {
			sprint, err := s.store.GetSprint(input.SprintID)
			if err != nil {
				return nil, &ValidationError{Field: "sprint_id", Message: "sprint not found: " + input.SprintID}
			}
			if sprint.Status == "inactive" {
				return nil, &ValidationError{Field: "sprint_id", Message: "cannot add tasks to an inactive sprint"}
			}
		} else {
			// Feature not enabled — silently clear the association
			input.SprintID = ""
		}
	}

	// Validate project association
	if input.ProjectID != "" {
		if s.feature != nil && s.feature.IsEnabled("projects") {
			if _, err := s.store.GetProject(input.ProjectID); err != nil {
				return nil, &ValidationError{Field: "project_id", Message: "project not found: " + input.ProjectID}
			}
		} else {
			input.ProjectID = ""
		}
	}

	// Validate epic association
	if input.EpicID != "" {
		if s.feature != nil && s.feature.IsEnabled("epics") {
			if _, err := s.store.GetEpic(input.EpicID); err != nil {
				return nil, &ValidationError{Field: "epic_id", Message: "epic not found: " + input.EpicID}
			}
		} else {
			input.EpicID = ""
		}
	}

	id, err := s.store.NextTaskID()
	if err != nil {
		return nil, err
	}

	rec := &sqlstore.TaskRecord{
		ID:                id,
		Title:             input.Title,
		Description:       input.Description,
		Priority:          priority,
		Manual:            input.Manual,
		Executor:          orDefault(input.Executor, "cli"),
		AgentProfile:      input.AgentProfile,
		WorkingDir:        input.WorkingDir,
		SystemPrompt:      input.SystemPrompt,
		MaxRetries:        orDefaultInt(input.MaxRetries, 3),
		OnDone:            input.OnDone,
		OnFail:            input.OnFail,
		OnReview:          input.OnReview,
		OnDoneMerge:       input.OnDoneMerge,
		DeliverablePreset: input.DeliverablePreset,
	}

	if len(input.Tools) > 0 {
		rec.Tools = sql.NullString{String: marshalJSON(input.Tools), Valid: true}
	}
	if len(input.Files) > 0 {
		rec.Files = sql.NullString{String: marshalJSON(input.Files), Valid: true}
	}
	if len(input.DependsOn) > 0 {
		rec.DependsOn = sql.NullString{String: marshalJSON(input.DependsOn), Valid: true}
	}
	if input.CostBudget != nil {
		rec.CostBudget = sql.NullFloat64{Float64: *input.CostBudget, Valid: true}
	}
	if input.SprintID != "" {
		rec.SprintID = sql.NullString{String: input.SprintID, Valid: true}
	}
	if input.ProjectID != "" {
		rec.ProjectID = sql.NullString{String: input.ProjectID, Valid: true}
	}
	if input.EpicID != "" {
		rec.EpicID = sql.NullString{String: input.EpicID, Valid: true}
	}

	if err := s.store.CreateTask(rec); err != nil {
		return nil, err
	}
	return s.store.GetTask(id)
}

// Get fetches a task by ID.
func (s *TaskService) Get(id string) (*sqlstore.TaskRecord, error) {
	return s.store.GetTask(id)
}

// List returns tasks matching the filter.
func (s *TaskService) List(filter sqlstore.TaskFilter) ([]sqlstore.TaskRecord, error) {
	return s.store.ListTasks(filter)
}

// Update applies a partial update to a task.
func (s *TaskService) Update(id string, update sqlstore.TaskUpdate) error {
	return s.store.UpdateTask(id, update)
}

// Delete removes a task by ID.
func (s *TaskService) Delete(id string) error {
	return s.store.DeleteTask(id)
}

// Transition moves a task to a new status if the FSM allows it.
func (s *TaskService) Transition(id, newStatus string) error {
	task, err := s.store.GetTask(id)
	if err != nil {
		return err
	}

	allowed, ok := validTransitions[task.Status]
	if !ok {
		return &TransitionError{From: task.Status, To: newStatus, Message: "unknown source status"}
	}
	for _, a := range allowed {
		if a == newStatus {
			return s.store.TransitionTask(id, newStatus)
		}
	}
	return &TransitionError{
		From:    task.Status,
		To:      newStatus,
		Message: "transition not permitted",
	}
}

// Search performs a text search over tasks.
func (s *TaskService) Search(query string) ([]sqlstore.TaskRecord, error) {
	return s.store.SearchTasks(query)
}

// BulkTransition applies the same status transition to multiple tasks.
// It returns the count of successful transitions and a slice of errors for failures.
func (s *TaskService) BulkTransition(ids []string, newStatus string) (int, []error) {
	var errs []error
	success := 0
	for _, id := range ids {
		if err := s.Transition(id, newStatus); err != nil {
			errs = append(errs, err)
		} else {
			success++
		}
	}
	return success, errs
}
