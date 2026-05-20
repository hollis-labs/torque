package scheduler

import (
	_ "embed"
)

// embeddedWorkerTemplate is the canonical ModeLongLived kind=agent worker
// boot contract. Embedded into the binary so the substrate's worker contract
// is always available regardless of which working directory the daemon was
// launched from. Mirrored shape against default-orchestrator.md (which the
// orchestrator-class loopback's planted boot reads) and default-end-agent.md
// (which the reviewer's loopback reads).
//
// CW-20260519-0095 / Phase 2 of the worker-substrate rebuild — workers
// historically booted with no completion contract whatsoever; this template
// supplies the four-part "done" definition (commit + push + verify +
// self-transition) and the help-asking primitive (`torque_task_checkpoint_emit`
// against the worker's own task surface).
//
//go:embed templates/default-worker.md
var embeddedWorkerTemplate string

// DefaultWorkerTemplate returns the embedded ModeLongLived worker boot
// contract. The agent package calls this from composeSystemPrompt when the
// dispatched session is a long-lived worker (Mode=ModeLongLived, role NOT
// in the orchestrator-class set). Returning the value through a function
// rather than a constant keeps callers from having to import an internal
// var name; the embed itself stays unexported.
func DefaultWorkerTemplate() string {
	return embeddedWorkerTemplate
}
