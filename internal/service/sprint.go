package service

import (
	"database/sql"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// SprintService provides business logic for sprints.
type SprintService struct {
	store   *sqlstore.Store
	feature *FeatureService
	task    *TaskService
}

// SprintCreateInput holds user-facing fields for creating a sprint.
type SprintCreateInput struct {
	Name         string
	ApprovalMode string
	CostBudget   *float64
	ProjectID    string // optional
}

// validSprintTransitions defines the sprint status FSM.
var validSprintTransitions = map[string][]string{
	"active":   {"inactive"},
	"inactive": {"active"},
}

// validApprovalModes lists the allowed approval mode values.
var validApprovalModes = map[string]bool{
	"auto":           true,
	"approve_sprint": true,
	"approve_each":   true,
}

// Create validates and creates a new sprint.
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

// Get fetches a sprint by ID.
func (s *SprintService) Get(id string) (*sqlstore.SprintRecord, error) {
	if err := s.feature.Require("sprints"); err != nil {
		return nil, err
	}
	return s.store.GetSprint(id)
}

// List returns sprints optionally filtered by status and projectID.
func (s *SprintService) List(status, projectID string) ([]sqlstore.SprintRecord, error) {
	if err := s.feature.Require("sprints"); err != nil {
		return nil, err
	}
	return s.store.ListSprints(sqlstore.SprintFilter{Status: status, ProjectID: projectID})
}

// Update applies a partial update to a sprint.
func (s *SprintService) Update(id string, update sqlstore.SprintUpdate) error {
	if err := s.feature.Require("sprints"); err != nil {
		return err
	}
	if update.ApprovalMode != nil && !validApprovalModes[*update.ApprovalMode] {
		return &ValidationError{
			Field:   "approval_mode",
			Message: "must be one of: auto, approve_sprint, approve_each",
		}
	}
	return s.store.UpdateSprint(id, update)
}

// Delete removes a sprint by ID.
func (s *SprintService) Delete(id string) error {
	if err := s.feature.Require("sprints"); err != nil {
		return err
	}
	return s.store.DeleteSprint(id)
}

// Transition moves a sprint to a new status if the FSM allows it.
func (s *SprintService) Transition(id, newStatus string) error {
	if err := s.feature.Require("sprints"); err != nil {
		return err
	}

	sprint, err := s.store.GetSprint(id)
	if err != nil {
		return err
	}

	allowed, ok := validSprintTransitions[sprint.Status]
	if !ok {
		return &TransitionError{From: sprint.Status, To: newStatus, Message: "unknown current status"}
	}

	valid := false
	for _, a := range allowed {
		if a == newStatus {
			valid = true
			break
		}
	}
	if !valid {
		return &TransitionError{From: sprint.Status, To: newStatus, Message: "transition not allowed"}
	}

	return s.store.TransitionSprint(id, newStatus)
}

// CheckCostBudget returns whether the sprint is within its cost budget
// and how much budget remains. Returns (true, remaining) if under budget
// or no budget set. Returns (false, remaining) if over budget.
func (s *SprintService) CheckCostBudget(sprintID string) (bool, float64) {
	sprint, err := s.store.GetSprint(sprintID)
	if err != nil {
		return true, 0
	}

	if !sprint.CostBudget.Valid {
		return true, 0 // No budget set — always OK
	}

	used, err := s.store.SprintCostUsed(sprintID)
	if err != nil {
		return true, sprint.CostBudget.Float64
	}

	remaining := sprint.CostBudget.Float64 - used
	return remaining > 0, remaining
}

// ApproveAll transitions all tasks in the sprint that are in "review" status
// to "done". Used with approval_mode = "approve_sprint".
func (s *SprintService) ApproveAll(sprintID string) (int, error) {
	if err := s.feature.Require("sprints"); err != nil {
		return 0, err
	}

	tasks, err := s.store.ListTasks(sqlstore.TaskFilter{
		SprintID: sprintID,
		Status:   "review",
	})
	if err != nil {
		return 0, err
	}

	count := 0
	for _, t := range tasks {
		if err := s.task.Transition(t.ID, "done"); err == nil {
			count++
		}
	}
	return count, nil
}

// ApproveTask transitions a specific task in a sprint from "review" to "done".
// Validates the task belongs to the specified sprint.
func (s *SprintService) ApproveTask(sprintID, taskID string) error {
	if err := s.feature.Require("sprints"); err != nil {
		return err
	}

	task, err := s.store.GetTask(taskID)
	if err != nil {
		return err
	}

	if !task.SprintID.Valid || task.SprintID.String != sprintID {
		return &ValidationError{
			Field:   "task_id",
			Message: "task " + taskID + " is not in sprint " + sprintID,
		}
	}

	if task.Status != "review" {
		return &TransitionError{
			From:    task.Status,
			To:      "done",
			Message: "task must be in review status to approve",
		}
	}

	return s.task.Transition(taskID, "done")
}
