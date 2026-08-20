package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EntityTypeTask is the entity_type value for task comments. Defined as a
// constant so call sites don't fork on string literals as new entity types
// land (collection, epic, sprint, project, ...).
const EntityTypeTask = "task"

// EntityTypeProject/EntityTypeEpic/EntityTypeSprint are the entity_type
// values for comments attached directly to a project/epic/sprint (ADR-0004
// §5's Comment target inventory — Issue/Plan are already covered via
// EntityTypeTask since they're Task rows with kind=issue/plan).
const (
	EntityTypeProject = "project"
	EntityTypeEpic    = "epic"
	EntityTypeSprint  = "sprint"
)

// ValidCommentEntityTypes is the ADR-0004-sanctioned closed set of entity
// kinds a comment may be attached to. The comments table itself stays
// polymorphic/unvalidated at the schema level (no FK — see migration 019's
// rationale), but the service layer (CommentService.Add) enforces this set
// so torque_comment_add's documented "supported: task/project/epic/sprint"
// contract is actually true end-to-end, not just aspirational docstring
// text. Store-layer callers that intentionally bypass the service (see
// comments_test.go's TestPolymorphicComments_NonTaskEntity) are unaffected —
// this map is consulted only by the service layer.
var ValidCommentEntityTypes = map[string]bool{
	EntityTypeTask:    true,
	EntityTypeProject: true,
	EntityTypeEpic:    true,
	EntityTypeSprint:  true,
}

// CommentDatetimeLayout is the Go time layout matching SQLite's own
// CURRENT_TIMESTAMP output ("YYYY-MM-DD HH:MM:SS", space-separated, UTC, no
// zone suffix, whole-second precision) — comments.created_at/updated_at are
// both DATETIME columns written either via DEFAULT CURRENT_TIMESTAMP (never
// an explicit app-side value) or, for updated_at on edit, via
// commentUpdatedAtNow below, which pre-formats to this exact layout rather
// than binding a time.Time directly. Binding a raw time.Time would route
// through modernc.org/sqlite's own time.Time formatting
// ("YYYY-MM-DD HH:MM:SS.ffffff +0000 UTC" — a completely different text
// shape), silently breaking plain TEXT comparison in the cursor WHERE
// clause below. Exported so mcpadapter can format created_after/
// created_before filter args to this same shape. Defined independently of
// tasks.go's SQLiteDatetimeLayout (no compile-time dependency between the
// two files) — the two constants must stay value-identical, since both
// describe the one format SQLite itself writes.
const CommentDatetimeLayout = "2006-01-02 15:04:05"

// commentUpdatedAtNow returns the current UTC instant, whole-second
// precision, pre-formatted in CommentDatetimeLayout, for writing to
// comments.updated_at on edit.
func commentUpdatedAtNow() string {
	return time.Now().UTC().Truncate(time.Second).Format(CommentDatetimeLayout)
}

// ErrInvalidCommentCursor wraps a decode/type-conversion failure when
// turning a PRIM-001/DEC-001 cursor's opaque, string-encoded sort value
// into the correctly-typed SQL bind argument for comments' sort column.
// This only happens for a malformed or tampered cursor token —
// mcpadapter.mapServiceError maps it to arg_invalid rather than internal,
// since it's a caller input fault, not a server fault.
var ErrInvalidCommentCursor = errors.New("invalid cursor")

// ErrCommentNotFound is returned wrapped by GetComment/UpdateComment/
// DeleteComment when the comment id does not exist. Callers should use
// errors.Is(err, ErrCommentNotFound) to distinguish a missing comment from
// other storage errors.
var ErrCommentNotFound = errors.New("comment not found")

// CommentRecord mirrors the comments table row.
//
// As of migration 019 the comments table is polymorphic: a comment is keyed
// by (entity_type, entity_id) rather than tied to a task. The legacy task_id
// column has been removed; for task comments use entity_type="task" and
// entity_id=<task id>.
//
// As of migration 031, UpdatedAt tracks edits made via UpdateComment
// (ENT-COMMENT's new update/delete capability). Existing rows backfilled to
// created_at; new rows default to CURRENT_TIMESTAMP like created_at.
type CommentRecord struct {
	ID         int64     `json:"id"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Author     string    `json:"author"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CommentFilter holds optional filter criteria for SearchComments — the
// shared query builder both torque_comment_list and torque_comment_search
// funnel through (ADR-0004 §3: distinct tools, same underlying query
// primitive — see comment_tools.go's handleCommentList/handleCommentSearch
// for how the two apply different defaults on top of this one filter).
type CommentFilter struct {
	EntityType string // restrict to one entity type (e.g. "task")
	EntityID   string // restrict to one entity (paired with EntityType)
	// EntityIDs, when non-empty, OR-matches entity_id against this set
	// (paired with EntityType) instead of a single EntityID — answers
	// "every comment across this set of entity refs" (e.g. every task in
	// sprint X, once the caller has resolved that sprint's task ids via
	// torque_task_list). Ignored if EntityID is also set (EntityID wins).
	EntityIDs []string
	Author    string // exact match
	// Search is a substring match on content using SQLite's built-in LIKE
	// operator, which is case-insensitive for ASCII characters by default.
	// Non-ASCII case folding is not guaranteed — behavior depends on the
	// SQLite build's ICU support. For portable case-insensitive matching
	// across all inputs, callers should lower-case the value before passing.
	Search string
	// CreatedAfter/CreatedBefore restrict to comments with created_at in
	// [CreatedAfter, CreatedBefore] (inclusive on both ends when set).
	// Pre-formatted as CommentDatetimeLayout text by the caller (mirrors
	// commentCursorArg's rationale: binding pre-formatted TEXT avoids
	// modernc.org/sqlite's time.Time reformatting mismatch against what
	// CURRENT_TIMESTAMP actually wrote).
	CreatedAfter  string
	CreatedBefore string
	Limit         int

	// SortBy/SortDir/AfterSortValue/AfterID are PRIM-001/PRIM-002's cursor
	// pagination + sort fields (internal/service/pagination), mirroring
	// TaskFilter's equivalent fields in sqlstore/tasks.go. SortBy is
	// expected to already be validated by
	// internal/service/pagination.ValidateSortBy before reaching here — an
	// empty or unrecognized SortBy is treated as "no sort_by supplied" and
	// falls back to the original hardcoded `created_at DESC, id DESC`
	// order, preserving pre-PRIM-001 callers (comments_test.go's existing
	// SearchComments tests) unchanged.
	SortBy         string
	SortDir        string
	AfterSortValue string
	AfterID        string
}

// commentSelectCols is the shared column list for every comments SELECT —
// keeps GetComment/ListCommentsForEntity/SearchComments's Scan calls in
// lockstep with each other and with commentSortColumn's expectations.
const commentSelectCols = "id, entity_type, entity_id, author, content, created_at, updated_at"

func scanComment(row interface{ Scan(...any) error }) (CommentRecord, error) {
	var c CommentRecord
	err := row.Scan(&c.ID, &c.EntityType, &c.EntityID, &c.Author, &c.Content, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// AddComment inserts a new comment and populates ID + CreatedAt/UpdatedAt
// from the DB (both DEFAULT CURRENT_TIMESTAMP).
func (s *Store) AddComment(c *CommentRecord) error {
	if c.EntityType == "" {
		c.EntityType = EntityTypeTask
	}
	const q = `INSERT INTO comments (entity_type, entity_id, author, content) VALUES (?, ?, ?, ?)
		RETURNING id, created_at, updated_at`
	return s.db.QueryRow(q, c.EntityType, c.EntityID, c.Author, c.Content).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

// GetComment fetches a single comment by id. Returns an error wrapping
// ErrCommentNotFound when no row matches.
func (s *Store) GetComment(id int64) (*CommentRecord, error) {
	q := `SELECT ` + commentSelectCols + ` FROM comments WHERE id = ?`
	c, err := scanComment(s.ReadDB().QueryRow(q, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("comment %d not found: %w", id, ErrCommentNotFound)
		}
		return nil, err
	}
	return &c, nil
}

// UpdateComment overwrites a comment's content and bumps updated_at.
// Author-scoping (only the original author may edit) is enforced by
// CommentService.Update, one layer up — this is a plain, unconditional
// write. Returns an error wrapping ErrCommentNotFound if id doesn't exist.
func (s *Store) UpdateComment(id int64, content string) error {
	res, err := s.db.Exec(`UPDATE comments SET content = ?, updated_at = ? WHERE id = ?`, content, commentUpdatedAtNow(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("comment %d not found: %w", id, ErrCommentNotFound)
	}
	return nil
}

// DeleteComment hard-deletes a comment by id. Author-scoping is enforced by
// CommentService.Delete, one layer up. Returns an error wrapping
// ErrCommentNotFound if id doesn't exist.
func (s *Store) DeleteComment(id int64) error {
	res, err := s.db.Exec(`DELETE FROM comments WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("comment %d not found: %w", id, ErrCommentNotFound)
	}
	return nil
}

// ListCommentsForEntity returns all comments for the given (entity_type,
// entity_id), oldest first. Unpaginated — kept as the simple/stable primitive
// the HTTP API (`GET /tasks/{id}/comments`, `GET /comments`) and CommentService.List
// rely on. torque_comment_list (the MCP tool) uses the richer,
// filter+sort+cursor SearchComments instead — see comment_tools.go's
// handleCommentList.
func (s *Store) ListCommentsForEntity(entityType, entityID string) ([]CommentRecord, error) {
	q := `SELECT ` + commentSelectCols + ` FROM comments WHERE entity_type = ? AND entity_id = ? ORDER BY created_at ASC`

	rows, err := s.ReadDB().Query(q, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []CommentRecord
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
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

// commentSortColumn maps a sort_by value to its backing SQL column for
// SearchComments (PRIM-002). Mirrors taskSortColumn's contract: the MCP
// layer validates sort_by against an explicit allow-list
// (internal/service/pagination.ValidateSortBy) before ever reaching here,
// but this stays defensive — an empty or unrecognized sort_by both return
// "", which SearchComments treats as "no sort_by supplied" and falls back
// to the pre-PRIM-001 hardcoded `created_at DESC, id DESC` order. Comment
// only has one meaningful sortable column today (no priority/status
// equivalent exists on a comment) — created_at is both the default and the
// only allow-listed value at the MCP layer.
func commentSortColumn(sortBy string) string {
	if sortBy == "created_at" {
		return "created_at"
	}
	return ""
}

// commentCursorArg converts a cursor's string-encoded sort value (DEC-001's
// `sv` field) into the correctly-typed SQL bind argument for sortBy's
// column. Mirrors tasks.go's taskCursorArg: the original string sv (not a
// re-derived time.Time) is bound as-is so the WHERE-clause comparison is
// byte-for-byte TEXT vs TEXT against what SQLite's own CURRENT_TIMESTAMP
// wrote — see CommentDatetimeLayout's doc comment above for the full story.
func commentCursorArg(sortBy, sv string) (any, error) {
	switch sortBy {
	case "created_at":
		if _, err := time.Parse(CommentDatetimeLayout, sv); err != nil {
			return nil, fmt.Errorf("%w: sort value for %s must match %q: %v", ErrInvalidCommentCursor, sortBy, CommentDatetimeLayout, err)
		}
		return sv, nil
	default:
		return nil, fmt.Errorf("%w: unsupported sort_by %q", ErrInvalidCommentCursor, sortBy)
	}
}

// SearchComments returns comments matching the filter. Default order
// (f.SortBy == "") is created_at DESC, id DESC — the pre-PRIM-001 behavior,
// preserved for any caller that hasn't adopted sort_by/cursor. When
// f.SortBy is set (PRIM-002), order becomes `<sort column> <SortDir>, id
// ASC` and, if f.AfterID is also set, results are additionally filtered to
// rows after the cursor's (sort value, id) position (PRIM-001/DEC-001
// keyset pagination) — id tiebreak is always ascending regardless of
// SortDir, per DEC-001.
//
// Search, EntityType/EntityID, and Author are all optional at the store
// layer; the calling MCP/service layer enforces any required-field
// contract. This is the shared query builder for BOTH torque_comment_list
// and torque_comment_search (ADR-0004 §3 keeps them as distinct tools with
// different defaults, not a different query shape at this layer — see
// comment_tools.go).
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
	} else if len(f.EntityIDs) > 0 {
		placeholders := make([]string, len(f.EntityIDs))
		for i, id := range f.EntityIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		where = append(where, "entity_id IN ("+strings.Join(placeholders, ",")+")")
	}
	if f.Author != "" {
		where = append(where, "author = ?")
		args = append(args, f.Author)
	}
	if f.CreatedAfter != "" {
		where = append(where, "created_at >= ?")
		args = append(args, f.CreatedAfter)
	}
	if f.CreatedBefore != "" {
		where = append(where, "created_at <= ?")
		args = append(args, f.CreatedBefore)
	}

	// PRIM-002 sort column + PRIM-001 cursor predicate — see commentSortColumn's
	// doc comment for why sortCol == "" preserves the original hardcoded order.
	sortCol := commentSortColumn(f.SortBy)
	desc := strings.EqualFold(f.SortDir, "desc")
	if sortCol != "" && f.AfterID != "" {
		arg, err := commentCursorArg(f.SortBy, f.AfterSortValue)
		if err != nil {
			return nil, err
		}
		afterID, err := strconv.ParseInt(f.AfterID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: cursor id must be an integer: %v", ErrInvalidCommentCursor, err)
		}
		cmp := ">"
		if desc {
			cmp = "<"
		}
		// Tuple comparison (sortCol, id) > (arg, AfterID), or the two-clause
		// equivalent below — tiebreak on id ascending regardless of
		// SortDir, per DEC-001.
		where = append(where, fmt.Sprintf("(%s %s ? OR (%s = ? AND id > ?))", sortCol, cmp, sortCol))
		args = append(args, arg, arg, afterID)
	}

	q := `SELECT ` + commentSelectCols + ` FROM comments`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	if sortCol != "" {
		dir := "ASC"
		if desc {
			dir = "DESC"
		}
		q += fmt.Sprintf(" ORDER BY %s %s, id ASC", sortCol, dir)
	} else {
		// id is auto-increment and monotonically increasing within a second,
		// so it provides a stable tie-breaker when multiple rows share the
		// same created_at timestamp (common in tests and bulk inserts).
		q += " ORDER BY created_at DESC, id DESC"
	}
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
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}
