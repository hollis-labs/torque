package service

import (
	"database/sql"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// EpicService provides business logic for epics.
type EpicService struct {
	store   *sqlstore.Store
	feature *FeatureService
}

// EpicCreateInput holds user-facing fields for creating an epic.
type EpicCreateInput struct {
	Name        string
	Description string
	Priority    *int64
	ProjectID   string // optional
}

// EpicUpdateInput holds optional fields for updating an epic.
type EpicUpdateInput struct {
	Name        *string
	Description *string
	Status      *string
	Priority    *int64
	ProjectID   *string
}

// validEpicStatuses lists the allowed epic status values.
var validEpicStatuses = map[string]bool{
	"active":   true,
	"inactive": true,
}

// Create validates and creates a new epic.
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

// Get fetches an epic by ID.
func (s *EpicService) Get(id string) (*sqlstore.EpicRecord, error) {
	if err := s.feature.Require("epics"); err != nil {
		return nil, err
	}
	return s.store.GetEpic(id)
}

// List returns epics optionally filtered by status and projectID.
func (s *EpicService) List(status, projectID string) ([]sqlstore.EpicRecord, error) {
	if err := s.feature.Require("epics"); err != nil {
		return nil, err
	}
	return s.store.ListEpics(sqlstore.EpicFilter{Status: status, ProjectID: projectID})
}

// Update applies a partial update to an epic.
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

// Delete removes an epic by ID.
func (s *EpicService) Delete(id string) error {
	if err := s.feature.Require("epics"); err != nil {
		return err
	}
	return s.store.DeleteEpic(id)
}
