package service

// Unlimited is the sentinel value meaning "no cap" for the three nullable
// numeric task fields: CostBudget, MaxDurationMs, TokenBudget. Use this
// instead of a magic -1 at call sites.
const Unlimited = int64(-1)

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
// mean "field not present in this write" (so Update can skip rules for
// fields the caller didn't touch).
type taskWriteFields struct {
	OnDone        string
	OnFail        string
	OnReview      string
	OnDoneMerge   string
	CostBudget    *float64
	MaxRetries    *int
	MaxDurationMs *int64
	TokenBudget   *int64
	Deliverables  []Deliverable
	DependsOn     []string
}

// validateTaskWrites enforces write-time invariants shared between Create and
// Update. Returns nil on success or a *ValidationError on the first failed
// rule.
func (s *TaskService) validateTaskWrites(fields taskWriteFields) error {
	// Lifecycle enum validation. Empty string means "use default" — accepted.
	if fields.OnDone != "" && !validOnDone[fields.OnDone] {
		return &ValidationError{
			Field:   "on_done",
			Message: "invalid on_done: got '" + fields.OnDone + "', expected one of: close, review, notify",
		}
	}
	if fields.OnFail != "" && !validOnFail[fields.OnFail] {
		return &ValidationError{
			Field:   "on_fail",
			Message: "invalid on_fail: got '" + fields.OnFail + "', expected one of: retry, block, escalate, notify",
		}
	}
	if fields.OnReview != "" && !validOnReview[fields.OnReview] {
		return &ValidationError{
			Field:   "on_review",
			Message: "invalid on_review: got '" + fields.OnReview + "', expected one of: pause, notify, auto-approve",
		}
	}
	if fields.OnDoneMerge != "" && !validOnDoneMerge[fields.OnDoneMerge] {
		return &ValidationError{
			Field:   "on_done_merge",
			Message: "invalid on_done_merge: got '" + fields.OnDoneMerge + "', expected one of: none, auto, pr, auto-resolve",
		}
	}

	// Numeric bound validation with sentinel value support.
	if fields.CostBudget != nil {
		v := *fields.CostBudget
		// Allowed: -1 (unlimited), 0, or any positive value.
		// Rejected: < -1 or any negative other than -1.
		if v < -1 || (v > -1 && v < 0) {
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
	// dependency counts. Empty list is fine (no deps).
	for _, depID := range fields.DependsOn {
		if _, err := s.store.GetTask(depID); err != nil {
			return &ValidationError{
				Field:   "depends_on",
				Message: "task " + depID + " not found",
			}
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
