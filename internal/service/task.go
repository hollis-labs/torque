package service

import (
	"database/sql"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// validTransitions defines the allowed FSM transitions for task status.
var validTransitions = map[string][]string{
	"todo":    {"doing", "blocked", "paused", "archived"},
	"doing":   {"review", "done", "blocked", "paused", "todo", "archived"},
	"review":  {"done", "doing", "blocked", "paused", "archived"},
	"blocked": {"todo", "archived"},
	"paused":  {"todo", "archived"},
	"done":    {"archived"},
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

	// Project 2 canonical task fields. These are persisted by Create's
	// record build below and validated by validateTaskWrites.
	Permissions     map[string]any
	Environment     map[string]string
	MaxDurationMs   *int64
	TokenBudget     *int64
	EscalationChain []string
	QualityGates    []string
	Deliverables    []Deliverable
	BlockedReason   string
	Metadata        map[string]any
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

	// Validate write-time invariants (enums, numeric bounds, deliverables, depends_on).
	fields, err := extractCreateFields(input)
	if err != nil {
		return nil, err
	}
	if err := s.validateTaskWrites(fields); err != nil {
		return nil, err
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
		OnDone:            orDefault(input.OnDone, "review"),
		OnFail:            orDefault(input.OnFail, "retry"),
		OnReview:          orDefault(input.OnReview, "pause"),
		OnDoneMerge:       orDefault(input.OnDoneMerge, "none"),
		DeliverablePreset: input.DeliverablePreset,
		BlockedReason:     input.BlockedReason,
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
	if len(input.Permissions) > 0 {
		rec.Permissions = sql.NullString{String: marshalJSON(input.Permissions), Valid: true}
	}
	if len(input.Environment) > 0 {
		rec.Environment = sql.NullString{String: marshalJSON(input.Environment), Valid: true}
	}
	if input.MaxDurationMs != nil {
		rec.MaxDurationMs = sql.NullInt64{Int64: *input.MaxDurationMs, Valid: true}
	}
	if input.TokenBudget != nil {
		rec.TokenBudget = sql.NullInt64{Int64: *input.TokenBudget, Valid: true}
	}
	if len(input.EscalationChain) > 0 {
		rec.EscalationChain = sql.NullString{String: marshalJSON(input.EscalationChain), Valid: true}
	}
	if len(input.QualityGates) > 0 {
		rec.QualityGates = sql.NullString{String: marshalJSON(input.QualityGates), Valid: true}
	}
	if len(input.Deliverables) > 0 {
		rec.Deliverables = sql.NullString{String: marshalJSON(input.Deliverables), Valid: true}
	}
	if len(input.Metadata) > 0 {
		rec.Metadata = sql.NullString{String: marshalJSON(input.Metadata), Valid: true}
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

	// Resolve tag names/slugs and link them to the task
	if len(input.Tags) > 0 {
		slugs, err := s.tags.ResolveNames(input.Tags)
		if err != nil {
			return nil, err
		}
		if err := s.store.SetTaskTags(id, slugs); err != nil {
			return nil, err
		}
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

// TaskUpdateInput wraps the store-level TaskUpdate and adds a tags field.
// The store-level TaskUpdate no longer carries tags because they live in
// the task_tags link table, not a column on tasks.
type TaskUpdateInput struct {
	sqlstore.TaskUpdate
	Tags *[]string // nil = no change; non-nil = replace all linked tags
}

// Update applies a partial update to a task. If Tags is non-nil, linked
// tags are resolved and replaced.
func (s *TaskService) Update(id string, input TaskUpdateInput) error {
	fields, err := extractUpdateFields(input)
	if err != nil {
		return err
	}
	if err := s.validateTaskWrites(fields); err != nil {
		return err
	}
	if err := s.store.UpdateTask(id, input.TaskUpdate); err != nil {
		return err
	}
	if input.Tags != nil {
		slugs, err := s.tags.ResolveNames(*input.Tags)
		if err != nil {
			return err
		}
		return s.store.SetTaskTags(id, slugs)
	}
	return nil
}

// Delete removes a task by ID.
func (s *TaskService) Delete(id string) error {
	return s.store.DeleteTask(id)
}

// ListTags returns the tags linked to a task.
func (s *TaskService) ListTags(taskID string) ([]sqlstore.TagRecord, error) {
	return s.store.ListTaskTags(taskID)
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
