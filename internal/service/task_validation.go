package service

import (
	"errors"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// Unlimited is the sentinel value meaning "no cap" for the three nullable
// numeric task fields: CostBudget, MaxDurationMs, TokenBudget. The constant
// is intentionally untyped so it can be used in either an int64 context
// (MaxDurationMs, TokenBudget) or a float64 context (CostBudget) without
// an explicit conversion at the call site.
const Unlimited = -1

// Deliverable declares an expected artifact a task must produce before it
// can be marked complete.
type Deliverable struct {
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

// validDeliverableTypes is the set of built-in artifact types that
// Deliverables.Type may take. The "custom" value is the plugin-extensibility
// escape hatch for plugin-defined types.
var validDeliverableTypes = map[string]bool{
	"diff":         true,
	"test-results": true,
	"screenshot":   true,
	"pr-link":      true,
	"branch":       true,
	"commit":       true,
	"log":          true,
	"finding":      true,
	"report":       true,
	"note":         true,
	"metrics":      true,
	"custom":       true,
}

// Lifecycle enum value sets per the canonical task model spec.
var validOnDone = map[string]bool{
	"close":  true,
	"review": true,
	"notify": true,
}

var validOnFail = map[string]bool{
	"retry":    true,
	"block":    true,
	"escalate": true,
	"notify":   true,
}

var validOnReview = map[string]bool{
	"pause":        true,
	"notify":       true,
	"auto-approve": true,
}

var validOnDoneMerge = map[string]bool{
	"none":         true,
	"auto":         true,
	"pr":           true,
	"auto-resolve": true,
}

// taskWriteFields is the internal projection of writable task fields shared
// between the Create and Update validation paths. Pointer fields use nil to
// mean "field not present in this write" so Update can skip rules for fields
// the caller didn't touch.
//
// The four lifecycle enum fields use *string instead of string so that
// validation can distinguish three states:
//   - nil           — field omitted (skip validation; for Create, the
//     record builder applies the canonical default via
//     orDefault; for Update, the column is left untouched)
//   - pointer to "" — field provided as an explicit empty string (rejected;
//     "use default" is expressed by omitting the field, not
//     by sending an empty string)
//   - pointer to v  — explicit value (validated against the allowed set)
type taskWriteFields struct {
	OnDone        *string
	OnFail        *string
	OnReview      *string
	OnDoneMerge   *string
	CostBudget    *float64
	MaxRetries    *int
	MaxDurationMs *int64
	TokenBudget   *int64
	Deliverables  []Deliverable
	DependsOn     []string
}

// validateTaskWrites enforces write-time invariants shared between Create and
// Update. Returns nil on success or a *ValidationError on the first failed
// rule. The DependsOn existence check uses errors.Is(err, sqlstore.ErrTaskNotFound)
// so transient store errors propagate as-is (and ultimately surface as a 500),
// while genuine missing-task errors become a 422 ValidationError.
func (s *TaskService) validateTaskWrites(fields taskWriteFields) error {
	if err := validateLifecycleEnum(fields.OnDone, "on_done", validOnDone, "close, review, notify"); err != nil {
		return err
	}
	if err := validateLifecycleEnum(fields.OnFail, "on_fail", validOnFail, "retry, block, escalate, notify"); err != nil {
		return err
	}
	if err := validateLifecycleEnum(fields.OnReview, "on_review", validOnReview, "pause, notify, auto-approve"); err != nil {
		return err
	}
	if err := validateLifecycleEnum(fields.OnDoneMerge, "on_done_merge", validOnDoneMerge, "none, auto, pr, auto-resolve"); err != nil {
		return err
	}

	// Numeric bound validation with sentinel value support.
	if fields.CostBudget != nil {
		v := *fields.CostBudget
		// Allowed: -1 (unlimited), 0, or any positive value.
		// Rejected: < -1 or any negative other than -1.
		if v < Unlimited || (v > Unlimited && v < 0) {
			return &ValidationError{
				Field:   "cost_budget",
				Message: "cost_budget must be -1 (unlimited), 0 (none), or a positive value",
			}
		}
	}
	if fields.MaxRetries != nil && *fields.MaxRetries < 0 {
		return &ValidationError{
			Field:   "max_retries",
			Message: "max_retries must be a non-negative integer",
		}
	}
	if fields.MaxDurationMs != nil {
		v := *fields.MaxDurationMs
		// Allowed: -1 (unlimited) or any positive value. Reject 0 and other negatives.
		if v != Unlimited && v <= 0 {
			return &ValidationError{
				Field:   "max_duration_ms",
				Message: "max_duration_ms must be -1 (unlimited) or a positive value in milliseconds",
			}
		}
	}
	if fields.TokenBudget != nil {
		v := *fields.TokenBudget
		if v != Unlimited && v <= 0 {
			return &ValidationError{
				Field:   "token_budget",
				Message: "token_budget must be -1 (unlimited) or a positive value",
			}
		}
	}

	// Deliverable type validation.
	for i, d := range fields.Deliverables {
		if !validDeliverableTypes[d.Type] {
			return &ValidationError{
				Field:   "deliverables",
				Message: "deliverables[" + intToString(i) + "].type '" + d.Type + "' is not a known artifact type",
			}
		}
	}

	// DependsOn existence validation. N+1 lookups; acceptable for typical
	// dependency counts. Empty list is fine (no deps). Only ErrTaskNotFound
	// becomes a ValidationError; other store errors propagate as-is so they
	// surface as 500s rather than misleading 422s.
	for _, depID := range fields.DependsOn {
		if _, err := s.store.GetTask(depID); err != nil {
			if errors.Is(err, sqlstore.ErrTaskNotFound) {
				return &ValidationError{
					Field:   "depends_on",
					Message: "task " + depID + " not found",
				}
			}
			return err
		}
	}

	return nil
}

// validateLifecycleEnum validates a single nullable lifecycle enum field.
// nil means "not set" (accepted; the record builder applies a default for
// Create, or the column is untouched for Update). An explicit empty string
// is rejected ("use default" must be expressed by omission). Any non-empty
// value must be in the allowed set.
func validateLifecycleEnum(value *string, fieldName string, allowed map[string]bool, allowedList string) error {
	if value == nil {
		return nil
	}
	if *value == "" {
		return &ValidationError{
			Field:   fieldName,
			Message: "invalid " + fieldName + ": empty string is not allowed; omit the field to use the default",
		}
	}
	if !allowed[*value] {
		return &ValidationError{
			Field:   fieldName,
			Message: "invalid " + fieldName + ": got '" + *value + "', expected one of: " + allowedList,
		}
	}
	return nil
}

// intToString is a tiny stdlib-only int-to-string helper to avoid pulling
// in strconv just for an error message position index.
func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	negative := i < 0
	if negative {
		i = -i
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

// nilIfEmpty returns nil if s is empty, or a pointer to s otherwise. Used by
// extractCreateFields to translate "empty string means use default" into the
// "*string nil means skip validation" convention used by taskWriteFields.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// extractCreateFields projects a TaskCreateInput into the validation helper's
// internal field set. Empty lifecycle enum strings are translated to nil so
// the validator skips them — Create's record builder will fill in defaults
// via orDefault before persisting.
func extractCreateFields(input TaskCreateInput) (taskWriteFields, error) {
	return taskWriteFields{
		OnDone:        nilIfEmpty(input.OnDone),
		OnFail:        nilIfEmpty(input.OnFail),
		OnReview:      nilIfEmpty(input.OnReview),
		OnDoneMerge:   nilIfEmpty(input.OnDoneMerge),
		CostBudget:    input.CostBudget,
		MaxRetries:    input.MaxRetries,
		MaxDurationMs: input.MaxDurationMs,
		TokenBudget:   input.TokenBudget,
		Deliverables:  input.Deliverables,
		DependsOn:     input.DependsOn,
	}, nil
}

// extractUpdateFields projects a TaskUpdateInput into the validation helper's
// internal field set. Only fields where the update pointer is non-nil get
// passed through — fields the caller didn't touch are left zero so the
// validator skips them.
//
// JSON-blob fields (Deliverables, DependsOn) are unmarshaled here so the
// validator can inspect their structured form. Malformed JSON returns a
// ValidationError immediately rather than being silently dropped, since the
// caller has explicitly provided the field for validation.
func extractUpdateFields(input TaskUpdateInput) (taskWriteFields, error) {
	f := taskWriteFields{
		OnDone:      input.OnDone,
		OnFail:      input.OnFail,
		OnReview:    input.OnReview,
		OnDoneMerge: input.OnDoneMerge,
		MaxRetries:  input.MaxRetries,
	}

	if input.CostBudget != nil && input.CostBudget.Valid {
		v := input.CostBudget.Float64
		f.CostBudget = &v
	}
	if input.MaxDurationMs != nil && input.MaxDurationMs.Valid {
		v := input.MaxDurationMs.Int64
		f.MaxDurationMs = &v
	}
	if input.TokenBudget != nil && input.TokenBudget.Valid {
		v := input.TokenBudget.Int64
		f.TokenBudget = &v
	}

	if input.Deliverables != nil && input.Deliverables.Valid && input.Deliverables.String != "" {
		var dels []Deliverable
		if err := unmarshalJSON([]byte(input.Deliverables.String), &dels); err != nil {
			return taskWriteFields{}, &ValidationError{
				Field:   "deliverables",
				Message: "invalid deliverables payload: " + err.Error(),
			}
		}
		f.Deliverables = dels
	}
	if input.DependsOn != nil && input.DependsOn.Valid && input.DependsOn.String != "" {
		var deps []string
		if err := unmarshalJSON([]byte(input.DependsOn.String), &deps); err != nil {
			return taskWriteFields{}, &ValidationError{
				Field:   "depends_on",
				Message: "invalid depends_on payload: " + err.Error(),
			}
		}
		f.DependsOn = deps
	}

	return f, nil
}
