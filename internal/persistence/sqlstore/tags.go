package sqlstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

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

// CreateTag inserts a new tag row.
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

// GetTag fetches a single tag by slug.
func (s *Store) GetTag(slug string) (*TagRecord, error) {
	row := s.db.QueryRow(`SELECT `+tagSelectCols+` FROM tags WHERE slug = ?`, slug)
	t, err := scanTag(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("tag %s not found", slug)
	}
	return t, err
}

// ListTags returns all tags ordered by name ASC (case-insensitive).
func (s *Store) ListTags() ([]TagRecord, error) {
	rows, err := s.db.Query(`SELECT ` + tagSelectCols + ` FROM tags ORDER BY name COLLATE NOCASE ASC`)
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

// UpdateTag applies a partial update. Used in Task 4.
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
		return fmt.Errorf("tag %s not found", slug)
	}
	return nil
}

// DeleteTag removes a tag by slug. CASCADE via FK removes task_tags rows. Used in Task 4.
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
		return fmt.Errorf("tag %s not found", slug)
	}
	return nil
}
