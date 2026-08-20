package service

import "fmt"

type NotFoundError struct {
	Entity string
	ID     string
}

func (e *NotFoundError) Error() string { return fmt.Sprintf("%s %s not found", e.Entity, e.ID) }

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s — %s", e.Field, e.Message)
}

type TransitionError struct {
	From    string
	To      string
	Message string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("cannot transition from %s to %s: %s", e.From, e.To, e.Message)
}

type FeatureDisabledError struct {
	Feature string
}

func (e *FeatureDisabledError) Error() string {
	return fmt.Sprintf("feature %q is not enabled — set features.%s = true in settings", e.Feature, e.Feature)
}

// ConflictError indicates a semantic conflict on a state-dependent operation,
// such as responding to an already-terminal checkpoint. HTTP callers should
// map this to 409.
type ConflictError struct {
	Message string
}

func (e *ConflictError) Error() string { return "conflict: " + e.Message }

// PermissionError indicates the caller is not authorized to perform the
// requested operation on an entity that DOES exist (contrast with
// NotFoundError). Introduced by ENT-COMMENT for author-scoped
// Comment.Update/Delete — mcpadapter.mapServiceError maps this to
// error.code=permission (ErrCodePermission), previously reserved but
// unmapped by any current error path.
type PermissionError struct {
	Message string
}

func (e *PermissionError) Error() string { return "permission denied: " + e.Message }
