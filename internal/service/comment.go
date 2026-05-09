package service

import (
	"context"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// CommentObserver is invoked synchronously after a comment is persisted.
// Implementations must be non-blocking — observers run on the call path of
// every clockwork_comment_add. Used by CW-20260509-0028 layer 2 to spot the
// orchestrator's session-complete marker.
type CommentObserver interface {
	ObserveComment(ctx context.Context, c *sqlstore.CommentRecord)
}

// CommentService provides business logic for entity comments.
//
// As of CW-20260503-0003 the comments table is polymorphic — comments are
// keyed by (entity_type, entity_id) rather than tied to a task. The Add and
// List helpers here mirror that shape; AddForTask / ListForTask remain as
// task-specific sugar where the call site only ever operates on tasks.
type CommentService struct {
	store    *sqlstore.Store
	observer CommentObserver // optional; nil disables the observer hook
}

// SetObserver installs a CommentObserver that runs after every successful Add.
// nil clears the observer. Not goroutine-safe with concurrent Add calls;
// install once at bootstrap before serving traffic.
func (s *CommentService) SetObserver(o CommentObserver) {
	s.observer = o
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
	if s.observer != nil {
		s.observer.ObserveComment(context.Background(), rec)
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
