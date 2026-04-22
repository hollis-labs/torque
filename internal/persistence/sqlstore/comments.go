package sqlstore

import (
	"fmt"
	"strings"
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

// CommentFilter holds optional filter criteria for SearchComments.
type CommentFilter struct {
	TaskID string // restrict to one task
	Author string // exact match
	Search string // case-insensitive substring match on content
	Limit  int
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

// SearchComments returns comments matching the filter, ordered by created_at DESC.
// Search, TaskID, and Author are all optional at the store layer; the calling
// MCP/service layer enforces any required-field contract.
func (s *Store) SearchComments(f CommentFilter) ([]CommentRecord, error) {
	var where []string
	var args []any

	if f.Search != "" {
		pattern := "%" + f.Search + "%"
		where = append(where, "content LIKE ?")
		args = append(args, pattern)
	}
	if f.TaskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, f.TaskID)
	}
	if f.Author != "" {
		where = append(where, "author = ?")
		args = append(args, f.Author)
	}

	q := `SELECT id, task_id, author, content, created_at FROM comments`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}

	rows, err := s.db.Query(q, args...)
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
