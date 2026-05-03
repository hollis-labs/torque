package service

import "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"

// CommentService provides business logic for entity comments.
//
// As of CW-20260503-0003 the comments table is polymorphic — comments are
// keyed by (entity_type, entity_id) rather than tied to a task. The Add and
// List helpers here mirror that shape; AddForTask / ListForTask remain as
// task-specific sugar where the call site only ever operates on tasks.
type CommentService struct {
	store *sqlstore.Store
}

// Add inserts a new comment on an arbitrary entity and returns the persisted
// record (including id and created_at populated by the DB).
func (s *CommentService) Add(entityType, entityID, author, content string) (*sqlstore.CommentRecord, error) {
	rec := &sqlstore.CommentRecord{
		EntityType: entityType,
		EntityID:   entityID,
		Author:     author,
		Content:    content,
	}
	if err := s.store.AddComment(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// AddForTask is sugar for Add(EntityTypeTask, taskID, …).
func (s *CommentService) AddForTask(taskID, author, content string) (*sqlstore.CommentRecord, error) {
	return s.Add(sqlstore.EntityTypeTask, taskID, author, content)
}

// List returns all comments for the given entity, oldest first.
func (s *CommentService) List(entityType, entityID string) ([]sqlstore.CommentRecord, error) {
	return s.store.ListCommentsForEntity(entityType, entityID)
}

// ListForTask is sugar for List(EntityTypeTask, taskID).
func (s *CommentService) ListForTask(taskID string) ([]sqlstore.CommentRecord, error) {
	return s.List(sqlstore.EntityTypeTask, taskID)
}

// Search returns comments matching the filter. The caller is responsible for
// enforcing any required-field contract (e.g. non-empty Search) at the
// MCP/HTTP layer — the store accepts an empty Search as "no content filter".
func (s *CommentService) Search(f sqlstore.CommentFilter) ([]sqlstore.CommentRecord, error) {
	return s.store.SearchComments(f)
}
