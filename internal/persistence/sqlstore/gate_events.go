package sqlstore

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
)

// Gate evaluations (CW-20261016-0134, T2). The evaluators (the lifecycle
// manager's acceptance checks and the landing gate) return a typed
// GateEvaluation; they do not emit. The code that owns the transition the
// evaluation leads to appends the events with AppendGateEvaluations in that
// transition's transaction, before the task.state_changed it causes.

// Gate results.
const (
	GatePass    = "pass"    // verified satisfied
	GateFail    = "fail"    // evaluated and unsatisfied
	GateBlocked = "blocked" // could not decide yet: lookup failure, pending checks
	GateSkipped = "skipped" // policy waived or bypassed, with a recorded reason
)

// Gate kinds Torque evaluates.
const (
	GateKindDeliverables = "deliverables"
	GateKindSubtodos     = "subtodos"
	GateKindLanding      = "landing"
)

// GateEvaluation is the outcome of one gate for one task.
//
// Result says what was found. Blocking says whether, under today's policy,
// that outcome stops acceptance: the two differ when an evaluation errored
// (Result blocked, Blocking false, because acceptance carries on as it did
// before gates emitted anything), and for a waiver (skipped, not blocking).
type GateEvaluation struct {
	Kind     string
	Result   string
	Blocking bool
	// Reason is a short human-readable explanation; Missing lists what was
	// unmet (deliverable types, subtodo ids, landing requirements).
	Reason  string
	Missing []string
}

// gatePayload is gate.evaluated@1 (urn:hollis-labs:events:gate.evaluated@1).
// reason and missing are extra fields, which the schema allows.
type gatePayload struct {
	Gate     gateRef     `json:"gate"`
	Subject  stateEntity `json:"subject"`
	Result   string      `json:"result"`
	Blocking bool        `json:"blocking"`
	Reason   string      `json:"reason,omitempty"`
	Missing  []string    `json:"missing,omitempty"`
}

type gateRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

const (
	maxGateMissing     = 20
	maxGateMissingItem = 240
)

// AppendGateEvaluations appends one gate.evaluated per evaluation, in order,
// in w's transaction. runID is the run the evaluation belongs to, or 0. The
// gate id is "<task id>:<kind>", stable across evaluations of the same gate;
// the subject is the task. Evidence is carried inline (reason, missing); no
// evidence artifact is stored.
//
// It returns the id of the deciding event, for the task.state_changed it leads
// to to name as its causation: the first blocking evaluation, else the last
// one. It is "" when there was nothing to append.
func (w *WriteTx) AppendGateEvaluations(taskID string, runID int64, evals []GateEvaluation, trigger Trigger) (string, error) {
	if len(evals) == 0 {
		return "", nil
	}
	scope, err := w.taskScope(taskID)
	if err != nil {
		return "", err
	}
	var deciding string
	blockingSeen := false
	if runID > 0 {
		scope.RunID = sql.NullInt64{Int64: runID, Valid: true}
	}
	for _, e := range evals {
		if e.Kind == "" {
			return "", fmt.Errorf("gate evaluation for %s: kind is required", taskID)
		}
		reason, truncated := sanitizeReason(e.Reason)
		var missing []string
		for i, m := range e.Missing {
			if i == maxGateMissing {
				truncated = true
				break
			}
			if len(m) > maxGateMissingItem {
				m, truncated = m[:maxGateMissingItem], true
			}
			missing = append(missing, m)
		}
		payload, err := json.Marshal(gatePayload{
			Gate:     gateRef{ID: taskID + ":" + e.Kind, Kind: e.Kind},
			Subject:  stateEntity{Type: "task", ID: taskID},
			Result:   e.Result,
			Blocking: e.Blocking,
			Reason:   reason,
			Missing:  missing,
		})
		if err != nil {
			return "", fmt.Errorf("gate event payload: %w", err)
		}
		id, err := w.appendEvent(eventSpec{
			Kind:       catalog.KindGateEvaluated,
			Category:   "state",
			EntityType: "task",
			EntityID:   taskID,
			Payload:    payload,
			Truncated:  truncated,
			Trigger:    trigger,
			Scope:      scope,
		})
		if err != nil {
			return "", err
		}
		if !blockingSeen {
			deciding = id
			blockingSeen = e.Blocking
		}
	}
	return deciding, nil
}
