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
// includeArchived=false (the default) excludes archived rows; pass true to
// surface them too.
//
// This is the pre-PRIM-001/002 signature, kept unchanged for its existing
// callers (HTTP's listEpics). New callers that need free-text search,
// sort, or cursor pagination should use ListPaginated instead.
func (s *EpicService) List(status, projectID string, includeArchived bool) ([]sqlstore.EpicRecord, error) {
	if err := s.feature.Require("epics"); err != nil {
		return nil, err
	}
	return s.store.ListEpics(sqlstore.EpicFilter{Status: status, ProjectID: projectID, IncludeArchived: includeArchived})
}

// EpicListInput holds torque_epic_list's full filter/search/sort/cursor
// surface (ADR-0004 §5: merged search, sort, cursor in one tool). SortBy/
// SortDir/AfterSortValue/AfterID are expected to already be validated by
// the caller (mcpadapter validates sort_by/sort_dir against an allow-list
// via internal/service/pagination before building this input, mirroring
// Task's handleTaskList).
type EpicListInput struct {
	Status          string
	ProjectID       string
	Search          string
	IncludeArchived bool
	SortBy          string
	SortDir         string
	AfterSortValue  string
	AfterID         string
	Limit           int
}

// ListPaginated returns epics matching input's filters, applying PRIM-001
// cursor pagination and PRIM-002 sort on top of List's plain status/
// projectID/includeArchived filtering. Reference: TaskService.List /
// sqlstore.ListTasks (internal/mcpadapter/task_tools.go's handleTaskList).
func (s *EpicService) ListPaginated(input EpicListInput) ([]sqlstore.EpicRecord, error) {
	if err := s.feature.Require("epics"); err != nil {
		return nil, err
	}
	return s.store.ListEpics(sqlstore.EpicFilter{
		Status:          input.Status,
		ProjectID:       input.ProjectID,
		Search:          input.Search,
		IncludeArchived: input.IncludeArchived,
		Limit:           input.Limit,
		SortBy:          input.SortBy,
		SortDir:         input.SortDir,
		AfterSortValue:  input.AfterSortValue,
		AfterID:         input.AfterID,
	})
}

// Update applies a partial update to an epic.
func (s *EpicService) Update(id string, input EpicUpdateInput) error {
	if err := s.feature.Require("epics"); err != nil {
		return err
	}

	// name is the one truly-required field (mirrors Create's check). Now
	// that the MCP layer detects name presence-based (SWEEP-001, mirroring
	// FIX-001's fix for Task's title), an explicit "name": "" reaches here
	// and must be rejected with a clean ValidationError rather than
	// silently persisting an empty name.
	if input.Name != nil && *input.Name == "" {
		return &ValidationError{Field: "name", Message: "name cannot be cleared to empty"}
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

// Archive soft-deletes an epic by setting archived_at. Does not change
// status — archiving an epic isn't the same fact as the epic being "done".
func (s *EpicService) Archive(id string) error {
	if err := s.feature.Require("epics"); err != nil {
		return err
	}
	return s.store.ArchiveEpic(id)
}

// Unarchive restores a previously archived epic.
func (s *EpicService) Unarchive(id string) error {
	if err := s.feature.Require("epics"); err != nil {
		return err
	}
	return s.store.UnarchiveEpic(id)
}
