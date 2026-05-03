package service

import (
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// CollectionService provides business logic for the collections feature.
// All methods gate on the "collections" feature flag.
type CollectionService struct {
	store   *sqlstore.Store
	feature *FeatureService
}

// CollectionCreateInput holds user-facing fields for creating a collection.
type CollectionCreateInput struct {
	Name        string
	Description string
}

// CollectionUpdateInput holds optional fields for updating a collection.
type CollectionUpdateInput struct {
	Name        *string
	Description *string
}

// validCollectionStatusFilters lists accepted ListCollections status values.
// "all" / "" both mean no filter.
var validCollectionStatusFilters = map[string]bool{
	"":         true,
	"active":   true,
	"archived": true,
	"all":      true,
}

// Create validates and creates a new collection.
func (s *CollectionService) Create(input CollectionCreateInput) (*sqlstore.CollectionRecord, error) {
	if err := s.feature.Require("collections"); err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
	}

	id, err := s.store.NextCollectionID()
	if err != nil {
		return nil, err
	}

	record := &sqlstore.CollectionRecord{
		ID:          id,
		Name:        input.Name,
		Description: input.Description,
	}

	if err := s.store.CreateCollection(record); err != nil {
		return nil, err
	}
	return s.store.GetCollection(id)
}

// Get fetches a collection by ID.
func (s *CollectionService) Get(id string) (*sqlstore.CollectionRecord, error) {
	if err := s.feature.Require("collections"); err != nil {
		return nil, err
	}
	return s.store.GetCollection(id)
}

// LookupNames returns a map of collection_id → name for the given IDs in
// a single query. Used by task list/get handlers to surface collection
// names alongside collection_id without per-task lookups. Bypasses the
// feature gate intentionally — the read is cheap and not feature-bearing.
func (s *CollectionService) LookupNames(ids []string) (map[string]string, error) {
	return s.store.GetCollectionNames(ids)
}

// List returns collections optionally filtered by status (active|archived|all).
// Default is "active" (per ticket); empty string is treated as "active" too.
func (s *CollectionService) List(status string) ([]sqlstore.CollectionRecord, error) {
	if err := s.feature.Require("collections"); err != nil {
		return nil, err
	}
	if !validCollectionStatusFilters[status] {
		return nil, &ValidationError{
			Field:   "status",
			Message: "must be one of: active, archived, all",
		}
	}
	if status == "" {
		status = "active"
	}
	return s.store.ListCollections(sqlstore.CollectionFilter{Status: status})
}

// Update applies a partial update to a collection.
func (s *CollectionService) Update(id string, input CollectionUpdateInput) error {
	if err := s.feature.Require("collections"); err != nil {
		return err
	}
	update := sqlstore.CollectionUpdate{
		Name:        input.Name,
		Description: input.Description,
	}
	return s.store.UpdateCollection(id, update)
}

// Archive soft-deletes a collection by setting archived_at.
func (s *CollectionService) Archive(id string) error {
	if err := s.feature.Require("collections"); err != nil {
		return err
	}
	return s.store.ArchiveCollection(id)
}

// Unarchive restores a previously archived collection.
func (s *CollectionService) Unarchive(id string) error {
	if err := s.feature.Require("collections"); err != nil {
		return err
	}
	return s.store.UnarchiveCollection(id)
}

// AddTask assigns a task to a collection at the given position. position <= 0
// means append to the end.
func (s *CollectionService) AddTask(collectionID, taskID string, position int) error {
	if err := s.feature.Require("collections"); err != nil {
		return err
	}
	if collectionID == "" {
		return &ValidationError{Field: "collection_id", Message: "collection_id is required"}
	}
	if taskID == "" {
		return &ValidationError{Field: "task_id", Message: "task_id is required"}
	}
	return s.store.AddTaskToCollection(taskID, collectionID, position)
}

// RemoveTask returns a task to inbox (clears collection_id and
// collection_position; preserves added_to_collections_at).
//
// expectedCollectionID is enforced if non-empty: the operation only succeeds
// if the task is currently a member of that collection. Use this from
// scoped routes (DELETE /collections/{id}/tasks/{task_id}) so the route id
// is authoritative and a stale or wrong id is rejected rather than
// silently detaching the task from whatever collection it sits in. Pass
// "" to skip the scope check.
func (s *CollectionService) RemoveTask(taskID, expectedCollectionID string) error {
	if err := s.feature.Require("collections"); err != nil {
		return err
	}
	if taskID == "" {
		return &ValidationError{Field: "task_id", Message: "task_id is required"}
	}
	return s.store.RemoveTaskFromCollection(taskID, expectedCollectionID)
}

// ReorderTasks rewrites collection_position for each supplied task in the
// supplied order. Tasks must currently belong to collectionID.
func (s *CollectionService) ReorderTasks(collectionID string, orderedTaskIDs []string) error {
	if err := s.feature.Require("collections"); err != nil {
		return err
	}
	if collectionID == "" {
		return &ValidationError{Field: "collection_id", Message: "collection_id is required"}
	}
	if len(orderedTaskIDs) == 0 {
		return &ValidationError{Field: "task_ids", Message: "task_ids must be non-empty"}
	}
	return s.store.ReorderCollectionTasks(collectionID, orderedTaskIDs)
}

// MoveTask atomically moves a task to a different collection at the given
// position. position <= 0 = append.
func (s *CollectionService) MoveTask(taskID, targetCollectionID string, position int) error {
	if err := s.feature.Require("collections"); err != nil {
		return err
	}
	if taskID == "" {
		return &ValidationError{Field: "task_id", Message: "task_id is required"}
	}
	if targetCollectionID == "" {
		return &ValidationError{Field: "target_collection_id", Message: "target_collection_id is required"}
	}
	return s.store.MoveTaskToCollection(taskID, targetCollectionID, position)
}

// AddToInbox marks a task as participating in the collections world. Idempotent.
func (s *CollectionService) AddToInbox(taskID string) error {
	if err := s.feature.Require("collections"); err != nil {
		return err
	}
	if taskID == "" {
		return &ValidationError{Field: "task_id", Message: "task_id is required"}
	}
	return s.store.AddTaskToInbox(taskID)
}

// ListInboxTasks returns the inbox view: tasks added to collections world but
// not assigned to any collection.
func (s *CollectionService) ListInboxTasks() ([]sqlstore.TaskRecord, error) {
	if err := s.feature.Require("collections"); err != nil {
		return nil, err
	}
	return s.store.ListInboxTasks()
}

// ListCollectionTasks returns the tasks in a collection ordered by
// collection_position.
func (s *CollectionService) ListCollectionTasks(collectionID string) ([]sqlstore.TaskRecord, error) {
	if err := s.feature.Require("collections"); err != nil {
		return nil, err
	}
	if collectionID == "" {
		return nil, &ValidationError{Field: "collection_id", Message: "collection_id is required"}
	}
	return s.store.ListCollectionTasks(collectionID)
}
