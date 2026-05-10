package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrTagNotFound is returned wrapped by GetTag/UpdateTag/DeleteTag when the
// tag slug does not exist. Callers should use errors.Is(err, ErrTagNotFound)
// to distinguish missing tags from other storage errors.
var ErrTagNotFound = errors.New("tag not found")

// TagRecord mirrors the tags table row.
type TagRecord struct {
	Slug        string
	Name        string
	Description string
	Color       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TagUpdate holds optional fields to update; nil pointer = no change.
type TagUpdate struct {
	Name        *string
	Description *string
	Color       *string
}

const tagSelectCols = `slug, name, description, color, created_at, updated_at`

// scanTag scans a single row into a TagRecord.
func scanTag(row interface {
	Scan(...any) error
}) (*TagRecord, error) {
	var t TagRecord
	err := row.Scan(&t.Slug, &t.Name, &t.Description, &t.Color, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateTag inserts a new tag row. Returns an error if the slug already exists.
func (s *Store) CreateTag(t *TagRecord) error {
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now

	if t.Color == "" {
		t.Color = "zinc"
	}

	_, err := s.db.Exec(
		`INSERT INTO tags (slug, name, description, color, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		t.Slug, t.Name, t.Description, t.Color, t.CreatedAt, t.UpdatedAt,
	)
	return err
}

// CreateTagIfNotExists inserts a tag row only if no row with the given slug
// exists. Uses INSERT OR IGNORE so concurrent callers can both succeed without
// a UNIQUE constraint race. Returns nil on successful insert OR on silent skip.
// Callers that need the final row should follow up with GetTag.
func (s *Store) CreateTagIfNotExists(t *TagRecord) error {
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now

	if t.Color == "" {
		t.Color = "zinc"
	}

	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO tags (slug, name, description, color, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		t.Slug, t.Name, t.Description, t.Color, t.CreatedAt, t.UpdatedAt,
	)
	return err
}

// GetTag fetches a single tag by slug. Returns an error wrapping
// ErrTagNotFound if no row matches; callers can use errors.Is to detect.
func (s *Store) GetTag(slug string) (*TagRecord, error) {
	row := s.ReadDB().QueryRow(`SELECT `+tagSelectCols+` FROM tags WHERE slug = ?`, slug)
	t, err := scanTag(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("tag %s: %w", slug, ErrTagNotFound)
	}
	return t, err
}

// ListTags returns all tags ordered by name ASC (case-insensitive).
func (s *Store) ListTags() ([]TagRecord, error) {
	rows, err := s.ReadDB().Query(`SELECT ` + tagSelectCols + ` FROM tags ORDER BY name COLLATE NOCASE ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []TagRecord
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		tags = append(tags, *t)
	}
	return tags, rows.Err()
}

// UpdateTag applies a partial update. Returns an error wrapping
// ErrTagNotFound if the slug does not exist.
func (s *Store) UpdateTag(slug string, u TagUpdate) error {
	var sets []string
	var args []any

	if u.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *u.Name)
	}
	if u.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *u.Description)
	}
	if u.Color != nil {
		sets = append(sets, "color = ?")
		args = append(args, *u.Color)
	}

	if len(sets) == 0 {
		return nil
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC())
	args = append(args, slug)

	q := `UPDATE tags SET ` + strings.Join(sets, ", ") + ` WHERE slug = ?`
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("tag %s: %w", slug, ErrTagNotFound)
	}
	return nil
}

// DeleteTag removes a tag by slug. CASCADE via FK removes task_tags rows.
// Returns an error wrapping ErrTagNotFound if the slug does not exist.
func (s *Store) DeleteTag(slug string) error {
	res, err := s.db.Exec(`DELETE FROM tags WHERE slug = ?`, slug)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("tag %s: %w", slug, ErrTagNotFound)
	}
	return nil
}

// SetTaskTags replaces all tags linked to the given task in a single transaction.
// Preserves input order via explicit sort_order (0-indexed). An empty slugs slice
// clears all tags on the task.
func (s *Store) SetTaskTags(taskID string, slugs []string) error {
	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM task_tags WHERE task_id = ?`, taskID); err != nil {
		return err
	}

	now := time.Now().UTC()
	for i, slug := range slugs {
		_, err := tx.Exec(
			`INSERT INTO task_tags (task_id, tag_slug, sort_order, created_at)
			 VALUES (?, ?, ?, ?)`,
			taskID, slug, i, now,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ListTaskTags returns all tags linked to a task, ordered by the user's
// assignment order (sort_order ASC).
func (s *Store) ListTaskTags(taskID string) ([]TagRecord, error) {
	rows, err := s.ReadDB().Query(
		`SELECT `+prefixCols("t.", tagSelectCols)+`
		 FROM tags t
		 JOIN task_tags tt ON tt.tag_slug = t.slug
		 WHERE tt.task_id = ?
		 ORDER BY tt.sort_order ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []TagRecord
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		tags = append(tags, *t)
	}
	return tags, rows.Err()
}

// MergeTags folds the source tag into the destination.
// All task_tags rows pointing at source are rewritten to dest.
// If a task already has both source and dest, the source row is dropped
// (dest wins). Finally the source tag row is deleted.
// Both tags must exist; source must differ from dest.
func (s *Store) MergeTags(sourceSlug, destSlug string) error {
	if sourceSlug == destSlug {
		return fmt.Errorf("merge source and destination cannot be the same")
	}
	// Verify both exist before touching anything
	if _, err := s.GetTag(sourceSlug); err != nil {
		return err
	}
	if _, err := s.GetTag(destSlug); err != nil {
		return err
	}

	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Remove source rows for tasks that already have the destination
	if _, err := tx.Exec(
		`DELETE FROM task_tags
		 WHERE tag_slug = ?
		   AND task_id IN (SELECT task_id FROM task_tags WHERE tag_slug = ?)`,
		sourceSlug, destSlug,
	); err != nil {
		return err
	}

	// Rewrite remaining source rows to dest
	if _, err := tx.Exec(
		`UPDATE task_tags SET tag_slug = ? WHERE tag_slug = ?`,
		destSlug, sourceSlug,
	); err != nil {
		return err
	}

	// Delete the source tag itself
	if _, err := tx.Exec(`DELETE FROM tags WHERE slug = ?`, sourceSlug); err != nil {
		return err
	}

	return tx.Commit()
}

// prefixCols prefixes a comma-separated column list with a table alias.
// e.g. prefixCols("t.", "slug, name") → "t.slug, t.name"
func prefixCols(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(out, ", ")
}
