package mcpadapter

// Internal tests (same package) so the mapper can be exercised directly
// without round-tripping through the MCP server. End-to-end error-code
// assertions live in errors_integration_test.go (external _test package).

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// TestMapServiceError_Table pins the classification rules in errors.go.
// A small, deliberate matrix: one input per code, one obvious miss for
// the internal fallback, and one ambiguous-but-documented string-match.
func TestMapServiceError_Table(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantCode  ErrorCode
		wantField string
	}{
		{
			name:     "nil_is_internal_unknown",
			err:      nil,
			wantCode: ErrCodeInternal,
		},
		{
			name:     "sqlstore_ErrTaskNotFound_wraps_to_not_found",
			err:      fmt.Errorf("task %s: %w", "T-1", sqlstore.ErrTaskNotFound),
			wantCode: ErrCodeNotFound,
		},
		{
			name:     "sqlstore_ErrTemplateNotFound_wraps_to_not_found",
			err:      fmt.Errorf("foo@1: %w", sqlstore.ErrTemplateNotFound),
			wantCode: ErrCodeNotFound,
		},
		{
			name:     "sqlstore_ErrCheckpointNotFound_wraps_to_not_found",
			err:      fmt.Errorf("corr: %w", sqlstore.ErrCheckpointNotFound),
			wantCode: ErrCodeNotFound,
		},
		{
			name:     "sqlstore_ErrArtifactNotFound_wraps_to_not_found",
			err:      fmt.Errorf("a 1: %w", sqlstore.ErrArtifactNotFound),
			wantCode: ErrCodeNotFound,
		},
		{
			name:     "sqlstore_ErrTagNotFound_wraps_to_not_found",
			err:      fmt.Errorf("tag x: %w", sqlstore.ErrTagNotFound),
			wantCode: ErrCodeNotFound,
		},
		{
			name:     "sqlstore_ErrTemplateReferenced_is_conflict",
			err:      fmt.Errorf("wrapped: %w", sqlstore.ErrTemplateReferenced),
			wantCode: ErrCodeConflict,
		},
		{
			name:     "service_NotFoundError_is_not_found",
			err:      &service.NotFoundError{Entity: "task", ID: "T-1"},
			wantCode: ErrCodeNotFound,
		},
		{
			name:      "service_ValidationError_is_arg_invalid_with_field",
			err:       &service.ValidationError{Field: "priority", Message: "out of range"},
			wantCode:  ErrCodeArgInvalid,
			wantField: "priority",
		},
		{
			name:     "service_TransitionError_is_conflict",
			err:      &service.TransitionError{From: "done", To: "doing", Message: "terminal"},
			wantCode: ErrCodeConflict,
		},
		{
			name:     "service_ConflictError_is_conflict",
			err:      &service.ConflictError{Message: "already responded"},
			wantCode: ErrCodeConflict,
		},
		{
			name:     "service_FeatureDisabledError_is_domain",
			err:      &service.FeatureDisabledError{Feature: "sprints"},
			wantCode: ErrCodeDomain,
		},
		{
			name:     "untyped_sprint_not_found_string_match_is_not_found",
			err:      errors.New("sprint SP-1 not found"),
			wantCode: ErrCodeNotFound,
		},
		{
			name:     "untyped_project_not_found_string_match_is_not_found",
			err:      errors.New("project PRJ-1 not found"),
			wantCode: ErrCodeNotFound,
		},
		{
			name:     "untyped_epic_not_found_string_match_is_not_found",
			err:      errors.New("epic EP-1 not found"),
			wantCode: ErrCodeNotFound,
		},
		{
			name:     "unmapped_error_falls_through_to_internal",
			err:      errors.New("random database goof — shouldn't leak"),
			wantCode: ErrCodeInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, field := mapServiceError(tc.err)
			assert.Equal(t, tc.wantCode, code, "code mismatch")
			assert.Equal(t, tc.wantField, field, "field mismatch")
		})
	}
}

// TestMapServiceError_InternalSanitizes verifies the sanitization rule:
// internal errors return the generic "internal server error" message
// instead of the caller-supplied error text. The underlying error text
// is logged server-side (log.Printf in mapServiceError) and MUST NOT
// ride back to the MCP caller in the `message` field.
func TestMapServiceError_InternalSanitizes(t *testing.T) {
	code, msg, _ := mapServiceError(errors.New("panic: runtime error: invalid memory address or nil pointer dereference [stack trace elided]"))
	require.Equal(t, ErrCodeInternal, code)
	assert.Equal(t, "internal server error", msg,
		"internal errors MUST return a short user-safe message; stack traces must not leak to the MCP caller")
}
