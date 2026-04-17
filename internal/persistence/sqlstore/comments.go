package sqlstore

import (
	"time"
)

// CommentRecord mirrors the comments table row.
type CommentRecord struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"task_id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// AddComment inserts a new comment and populates ID + CreatedAt from the DB.
func (s *Store) AddComment(c *CommentRecord) error {
	const q = `INSERT INTO comments (task_id, author, content) VALUES (?, ?, ?)
		RETURNING id, created_at`
	return s.db.QueryRow(q, c.TaskID, c.Author, c.Content).Scan(&c.ID, &c.CreatedAt)
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
