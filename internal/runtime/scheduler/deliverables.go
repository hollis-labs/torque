package scheduler

import (
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// DeliverableChecker validates that required artifacts are present
// before allowing a task status transition.
type DeliverableChecker struct{}

// NewDeliverableChecker creates a new deliverable checker.
func NewDeliverableChecker() *DeliverableChecker {
	return &DeliverableChecker{}
}

// Check returns the list of required deliverables that are missing from the artifacts.
// Only deliverables with Required=true are checked.
func (d *DeliverableChecker) Check(required []executor.Deliverable, artifacts []executor.Artifact) []executor.Deliverable {
	if len(required) == 0 {
		return nil
	}

	// Build a set of artifact types present
	present := make(map[string]bool)
	for _, a := range artifacts {
		present[a.Type] = true
	}

	var missing []executor.Deliverable
	for _, req := range required {
		if !req.Required {
			continue
		}
		if !present[req.Type] {
			missing = append(missing, req)
		}
	}

	return missing
}

// HasAllRequired returns true if all required deliverables are present.
func (d *DeliverableChecker) HasAllRequired(required []executor.Deliverable, artifacts []executor.Artifact) bool {
	return len(d.Check(required, artifacts)) == 0
}
