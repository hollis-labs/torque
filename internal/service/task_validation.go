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
