package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/go-strutil"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

// TagService provides business logic for tags.
type TagService struct {
	store *sqlstore.Store
}

// TagCreateInput holds fields for creating a tag.
type TagCreateInput struct {
	Name        string
	Slug        string
	Description string
	Color       string
}

// TagUpdateInput is a re-export of the store-layer update type.
type TagUpdateInput = sqlstore.TagUpdate

type TagListInput struct {
	Query     string
	Color     string
	Limit     int
	AfterName string
	AfterSlug string
}

type TagListResult struct {
	Tags             []sqlstore.TagRecord
	Limit            int
	Total            int
	HasMoreFromQuery bool
}

const (
	DefaultTagListLimit = 50
	MaxTagListLimit     = 200
	TagListSortBy       = "name"
	TagListSortDir      = "asc"
)

var validTagColors = map[string]bool{
	"zinc": true, "red": true, "orange": true, "amber": true,
	"green": true, "teal": true, "blue": true, "violet": true, "pink": true,
}

// isValidSlug validates explicit slug input: lowercase alphanumeric + hyphens,
// no leading/trailing/consecutive hyphens, 1-48 chars. Implemented by round-tripping
// through Slugify and comparing — a slug that normalizes to itself is, by definition,
// in canonical form.
func isValidSlug(s string) bool {
	if len(s) < 1 || len(s) > 48 {
		return false
	}
	return strutil.Slugify(s) == s
}

// validateColor checks a color string against the palette.
// Empty color is replaced with the default "zinc".
func validateColor(c string) (string, error) {
	if c == "" {
		return "zinc", nil
	}
	if !validTagColors[c] {
		return "", &ValidationError{
			Field:   "color",
			Message: "invalid color; must be one of zinc, red, orange, amber, green, teal, blue, violet, pink",
		}
	}
	return c, nil
}

type sqlStateError interface {
	SQLState() string
}

func IsTagUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	var sqlState sqlStateError
	if errors.As(err, &sqlState) && sqlState.SQLState() == "23505" {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "SQLSTATE 23505") ||
		strings.Contains(msg, "SQLSTATE=23505")
}

// Create creates a new tag, deriving the slug from the name if not provided.
func (s *TagService) Create(input TagCreateInput) (*sqlstore.TagRecord, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
	}
	if len(name) > 64 {
		return nil, &ValidationError{Field: "name", Message: "name must be at most 64 characters"}
	}
	if len(input.Description) > 500 {
		return nil, &ValidationError{Field: "description", Message: "description must be at most 500 characters"}
	}

	color, err := validateColor(input.Color)
	if err != nil {
		return nil, err
	}

	slug := input.Slug
	if slug == "" {
		slug = strutil.Slugify(name)
		if slug == "" {
			return nil, &ValidationError{Field: "name", Message: "name does not produce a valid slug"}
		}
	} else if !isValidSlug(slug) {
		return nil, &ValidationError{Field: "slug", Message: "slug must be lowercase alphanumeric with hyphens, 1-48 chars"}
	}

	rec := &sqlstore.TagRecord{
		Slug:        slug,
		Name:        name,
		Description: input.Description,
		Color:       color,
	}
	if err := s.store.CreateTag(rec); err != nil {
		return nil, err
	}
	return s.store.GetTag(slug)
}

// Get returns a single tag by slug.
func (s *TagService) Get(slug string) (*sqlstore.TagRecord, error) {
	return s.store.GetTag(slug)
}

// List returns all tags.
func (s *TagService) List() ([]sqlstore.TagRecord, error) {
	return s.store.ListTags()
}

func (s *TagService) ListPage(input TagListInput) (TagListResult, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = DefaultTagListLimit
	}
	if limit > MaxTagListLimit {
		limit = MaxTagListLimit
	}

	tags, err := s.store.ListTagsPage(sqlstore.TagFilter{
		Query:     input.Query,
		Color:     input.Color,
		Limit:     limit + 1,
		AfterName: input.AfterName,
		AfterSlug: input.AfterSlug,
	})
	if err != nil {
		return TagListResult{}, err
	}
	hasMore := len(tags.Tags) > limit
	if hasMore {
		tags.Tags = tags.Tags[:limit]
	}
	return TagListResult{Tags: tags.Tags, Limit: limit, Total: tags.Total, HasMoreFromQuery: hasMore}, nil
}

func DecodeTagListCursor(raw string) (afterName, afterSlug string, err error) {
	if raw == "" {
		return "", "", nil
	}
	c, err := pagination.Decode(raw)
	if err != nil {
		return "", "", &ValidationError{Field: "cursor", Message: fmt.Sprintf("invalid cursor: %v", err)}
	}
	if err := c.Validate(TagListSortBy, TagListSortDir); err != nil {
		return "", "", &ValidationError{Field: "cursor", Message: err.Error()}
	}
	return c.SortValue, c.ID, nil
}

func TagListCursor(t sqlstore.TagRecord) string {
	return pagination.Encode(TagListSortBy, TagListSortDir, t.Name, t.Slug)
}

// Update applies a partial update to a tag. Does not allow changing the slug.
func (s *TagService) Update(slug string, u TagUpdateInput) (*sqlstore.TagRecord, error) {
	if u.Name != nil {
		name := strings.TrimSpace(*u.Name)
		if name == "" {
			return nil, &ValidationError{Field: "name", Message: "name cannot be blank"}
		}
		if len(name) > 64 {
			return nil, &ValidationError{Field: "name", Message: "name must be at most 64 characters"}
		}
		u.Name = &name
	}
	if u.Description != nil && len(*u.Description) > 500 {
		return nil, &ValidationError{Field: "description", Message: "description must be at most 500 characters"}
	}
	if u.Color != nil {
		c, err := validateColor(*u.Color)
		if err != nil {
			return nil, err
		}
		u.Color = &c
	}

	if err := s.store.UpdateTag(slug, u); err != nil {
		return nil, err
	}
	return s.store.GetTag(slug)
}

// Delete removes a tag by slug.
func (s *TagService) Delete(slug string) error {
	return s.store.DeleteTag(slug)
}

// Merge folds source into destination. Both must exist; source ≠ dest.
// Rewrites all task_tags links and deletes the source tag.
func (s *TagService) Merge(sourceSlug, destSlug string) error {
	if sourceSlug == destSlug {
		return &ValidationError{Field: "into", Message: "merge source and destination cannot be the same"}
	}
	return s.store.MergeTags(sourceSlug, destSlug)
}

// ResolveNames takes a list of tag names (or existing slugs), normalizes each
// via strutil.Slugify, skips any that normalize to empty, deduplicates while
// preserving first-occurrence order, and auto-creates any missing tags with
// the raw input as the display name (first-seen wins). Returns the slug list
// in input order.
func (s *TagService) ResolveNames(inputs []string) ([]string, error) {
	var result []string
	seen := make(map[string]bool)
	// Track the first-seen raw input for each new slug so we can use it as the name
	rawByFirstSlug := make(map[string]string)

	for _, raw := range inputs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		slug := strutil.Slugify(trimmed)
		if slug == "" {
			continue
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		result = append(result, slug)
		if _, exists := rawByFirstSlug[slug]; !exists {
			rawByFirstSlug[slug] = trimmed
		}
	}

	// Auto-create any missing tags. Uses CreateTagIfNotExists so that
	// concurrent requests resolving the same new name don't collide on the
	// UNIQUE(slug) constraint — the second caller silently no-ops.
	for _, slug := range result {
		name := rawByFirstSlug[slug]
		// Truncate name if the raw input exceeds 64 chars so we don't reject
		// a task-create just because a tag name was long.
		if len(name) > 64 {
			name = name[:64]
		}
		rec := &sqlstore.TagRecord{
			Slug:  slug,
			Name:  name,
			Color: "zinc",
		}
		if err := s.store.CreateTagIfNotExists(rec); err != nil {
			return nil, err
		}
	}

	return result, nil
}
