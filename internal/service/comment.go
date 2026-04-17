package service

import "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"

// CommentService provides business logic for task comments.
type CommentService struct {
	store *sqlstore.Store
}

// Add inserts a new comment on a task and returns the persisted record
// (including id and created_at populated by the DB).
func (s *CommentService) Add(taskID, author, content string) (*sqlstore.CommentRecord, error) {
	rec := &sqlstore.CommentRecord{
		TaskID:  taskID,
		Author:  author,
		Content: content,
	}
	if err := s.store.AddComment(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// List returns all comments for a task.
func (s *CommentService) List(taskID string) ([]sqlstore.CommentRecord, error) {
	return s.store.ListComments(taskID)
}
