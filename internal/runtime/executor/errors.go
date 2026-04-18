package executor

import "errors"

// PermanentError wraps an error that MUST NOT be retried by the scheduler.
// Config-permanent failures (missing agent profile, unresolvable command,
// malformed task shape, etc.) cannot be recovered by re-running the task —
// the scheduler treats a PermanentError surfaced from Validate() as an
// immediate block-no-retry signal so we don't waste retry budget and
// pollute the runs table.
//
// Scope is deliberately narrow: this taxonomy exists ONLY to distinguish
// the config-permanent class from transient failures. It is NOT a general
// error-taxonomy substrate — expand carefully if at all. See CW-20260418-0010.
type PermanentError struct {
	Err error
}

// NewPermanentError wraps err as permanent. Returns nil when err is nil so
// callers can unconditionally pass through their own error return.
func NewPermanentError(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// Error implements error.
func (e *PermanentError) Error() string {
	if e == nil || e.Err == nil {
		return "permanent error"
	}
	return e.Err.Error()
}

// Unwrap enables errors.Is / errors.As traversal to the wrapped cause.
func (e *PermanentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsPermanent reports whether err (or anything in its chain) is a
// PermanentError. Safe to call with nil.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	var pe *PermanentError
	return errors.As(err, &pe)
}
