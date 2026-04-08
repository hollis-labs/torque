package sqlstore

import (
	"time"
)

// CommentRecord mirrors the comments table row.
type CommentRecord struct {
	ID        int64
	TaskID    string
	Author    string
	Content   string
	CreatedAt time.Time
}

// AddComment inserts a new comment.
func (s *Store) AddComment(c *CommentRecord) error {
	const q = `INSERT INTO comments (task_id, author, content) VALUES (?, ?, ?)`

	res, err := s.db.Exec(q, c.TaskID, c.Author, c.Content)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	c.ID = id
	return nil
}

// ListComments returns all comments for a task, oldest first.
func (s *Store) ListComments(taskID string) ([]CommentRecord, error) {
	const q = `SELECT id, task_id, author, content, created_at
		FROM comments WHERE task_id = ? ORDER BY created_at ASC`

	rows, err := s.db.Query(q, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []CommentRecord
	for rows.Next() {
		var c CommentRecord
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Author, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}
