package service

import "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"

// CommentService provides business logic for task comments.
type CommentService struct {
	store *sqlstore.Store
}

// Add inserts a new comment on a task.
func (s *CommentService) Add(taskID, author, content string) error {
	rec := &sqlstore.CommentRecord{
		TaskID:  taskID,
		Author:  author,
		Content: content,
	}
	return s.store.AddComment(rec)
}

// List returns all comments for a task.
func (s *CommentService) List(taskID string) ([]sqlstore.CommentRecord, error) {
	return s.store.ListComments(taskID)
}
