package service

import "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"

// EpicService provides business logic for epics.
type EpicService struct {
	store   *sqlstore.Store
	feature *FeatureService
}

// EpicCreateInput holds user-facing fields for creating an epic.
type EpicCreateInput struct {
	Name        string
	Description string
}

// EpicUpdateInput holds optional fields for updating an epic.
type EpicUpdateInput struct {
	Name        *string
	Description *string
	Status      *string
}

// validEpicStatuses lists the allowed epic status values.
var validEpicStatuses = map[string]bool{
	"open":   true,
	"closed": true,
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
		Status:      "open",
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

// List returns epics optionally filtered by status.
func (s *EpicService) List(status string) ([]sqlstore.EpicRecord, error) {
	if err := s.feature.Require("epics"); err != nil {
		return nil, err
	}
	return s.store.ListEpics(sqlstore.EpicFilter{Status: status})
}

// Update applies a partial update to an epic.
func (s *EpicService) Update(id string, input EpicUpdateInput) error {
	if err := s.feature.Require("epics"); err != nil {
		return err
	}

	if input.Status != nil && !validEpicStatuses[*input.Status] {
		return &ValidationError{
			Field:   "status",
			Message: "must be one of: open, closed",
		}
	}

	update := sqlstore.EpicUpdate{
		Name:        input.Name,
		Description: input.Description,
		Status:      input.Status,
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
