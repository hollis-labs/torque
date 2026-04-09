package service

import (
	"strings"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/go-strutil"
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
