package pagination

import (
	"fmt"
	"strings"
)

// ValidateSortBy checks value against an entity's allow-list, case-
// insensitively, and returns the canonical lower-case form. Every Phase 4
// entity list tool is expected to call this with its own allow-list (e.g.
// Task: "priority", "status", "updated_at", "created_at"; Epic/Sprint/
// Project: "name", "status", "updated_at", "created_at") rather than
// hand-rolling the same switch/contains check per entity.
//
// An empty value is NOT validated here — "no sort_by given" and "invalid
// sort_by" are different outcomes (apply the entity's default vs reject
// with arg_invalid); callers should special-case the empty string and
// substitute their default sort_by before calling ValidateSortBy, or after
// a failed lookup depending on which reads more naturally at the call site.
func ValidateSortBy(value string, allowed ...string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", fmt.Errorf("sort_by must be one of: %s (got %q)", strings.Join(allowed, ", "), value)
}

// ValidateSortDir checks value is "asc" or "desc" (case-insensitive) and
// returns the canonical lower-case form. Like ValidateSortBy, an empty
// value is not defaulted here — callers substitute their own default
// sort_dir before calling this.
func ValidateSortDir(value string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	switch v {
	case "asc", "desc":
		return v, nil
	default:
		return "", fmt.Errorf(`sort_dir must be "asc" or "desc" (got %q)`, value)
	}
}
