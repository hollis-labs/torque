package sqlstore

import (
	"fmt"
	"strings"
	"time"
)

// EntityTypeTask is the entity_type value for task comments. Defined as a
// constant so call sites don't fork on string literals as new entity types
// land (collection, epic, sprint, project, ...).
const EntityTypeTask = "task"

// CommentRecord mirrors the comments table row.
//
// As of migration 019 the comments table is polymorphic: a comment is keyed
// by (entity_type, entity_id) rather than tied to a task. The legacy task_id
// column has been removed; for task comments use entity_type="task" and
// entity_id=<task id>.
type CommentRecord struct {
	ID         int64     `json:"id"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Author     string    `json:"author"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

// CommentFilter holds optional filter criteria for SearchComments.
type CommentFilter struct {
	EntityType string // restrict to one entity type (e.g. "task")
	EntityID   string // restrict to one entity (paired with EntityType)
	Author     string // exact match
	// Search is a substring match on content using SQLite's built-in LIKE
	// operator, which is case-insensitive for ASCII characters by default.
	// Non-ASCII case folding is not guaranteed — behavior depends on the
	// SQLite build's ICU support. For portable case-insensitive matching
	// across all inputs, callers should lower-case the value before passing.
	Search string
	Limit  int
}

// AddComment inserts a new comment and populates ID + CreatedAt from the DB.
func (s *Store) AddComment(c *CommentRecord) error {
	if c.EntityType == "" {
		c.EntityType = EntityTypeTask
	}
	const q = `INSERT INTO comments (entity_type, entity_id, author, content) VALUES (?, ?, ?, ?)
		RETURNING id, created_at`
	return s.db.QueryRow(q, c.EntityType, c.EntityID, c.Author, c.Content).Scan(&c.ID, &c.CreatedAt)
}

// ListCommentsForEntity returns all comments for the given (entity_type,
// entity_id), oldest first.
func (s *Store) ListCommentsForEntity(entityType, entityID string) ([]CommentRecord, error) {
	const q = `SELECT id, entity_type, entity_id, author, content, created_at
		FROM comments WHERE entity_type = ? AND entity_id = ? ORDER BY created_at ASC`

	rows, err := s.ReadDB().Query(q, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []CommentRecord
	for rows.Next() {
		var c CommentRecord
		if err := rows.Scan(&c.ID, &c.EntityType, &c.EntityID, &c.Author, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// ListComments is a thin wrapper over ListCommentsForEntity for the common
// task-comment case. Kept to avoid cluttering call sites that only ever
// operate on task comments.
func (s *Store) ListComments(taskID string) ([]CommentRecord, error) {
	return s.ListCommentsForEntity(EntityTypeTask, taskID)
}

// SearchComments returns comments matching the filter, ordered by created_at DESC.
// Search, EntityType/EntityID, and Author are all optional at the store layer;
// the calling MCP/service layer enforces any required-field contract.
func (s *Store) SearchComments(f CommentFilter) ([]CommentRecord, error) {
	var where []string
	var args []any

	if f.Search != "" {
		pattern := "%" + f.Search + "%"
		where = append(where, "content LIKE ?")
		args = append(args, pattern)
	}
	if f.EntityType != "" {
		where = append(where, "entity_type = ?")
		args = append(args, f.EntityType)
	}
	if f.EntityID != "" {
		where = append(where, "entity_id = ?")
		args = append(args, f.EntityID)
	}
	if f.Author != "" {
		where = append(where, "author = ?")
		args = append(args, f.Author)
	}

	q := `SELECT id, entity_type, entity_id, author, content, created_at FROM comments`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	// id is auto-increment and monotonically increasing within a second, so
	// it provides a stable tie-breaker when multiple rows share the same
	// created_at timestamp (common in tests and bulk inserts).
	q += " ORDER BY created_at DESC, id DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}

	rows, err := s.ReadDB().Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []CommentRecord
	for rows.Next() {
		var c CommentRecord
		if err := rows.Scan(&c.ID, &c.EntityType, &c.EntityID, &c.Author, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}
