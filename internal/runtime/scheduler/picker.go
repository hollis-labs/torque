package scheduler

import (
	"encoding/json"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// Skip reason tags emitted by the picker. These are the canonical keys the
// info-level per-tick counter log uses so operators can grep/sum across
// history. Adding a new reason: append here, add the branch in Pick, and
// (optionally) assert it in a test. Never rename an existing key — it
// breaks log-grep workflows.
const (
	SkipReasonManual             = "manual"
	SkipReasonParentKind         = "parent_kind"
	SkipReasonPlanKind           = "plan_kind"
	SkipReasonEmptyProfile       = "empty_profile"
	SkipReasonProjectBusy        = "project_busy"
	SkipReasonProjectContention  = "project_contention"
	SkipReasonDepUnmet           = "dep_unmet"
	SkipReasonDepMalformed       = "dep_malformed"
)

// SkipDecision records why the picker passed over a single candidate. One
// entry per skipped task; eligible tasks produce no SkipDecision.
type SkipDecision struct {
	TaskID string
	Reason string
}

// PickDecisions is the observability artifact produced by one Pick call.
// Counts is the per-reason tally (keys are the SkipReason* constants).
// Skipped carries per-task detail for debug-level logging. Candidates is
// the total number of todo-status rows considered this tick (including
// those that ended up picked).
type PickDecisions struct {
	Candidates int
	Counts     map[string]int
	Skipped    []SkipDecision
}

// newPickDecisions returns a zero-valued decisions struct with a non-nil
// Counts map so callers can increment without nil-checking.
func newPickDecisions() PickDecisions {
	return PickDecisions{Counts: make(map[string]int)}
}

// Picker selects tasks eligible for scheduling.
type Picker struct {
	store *sqlstore.Store
}

// NewPicker creates a new task picker.
func NewPicker(store *sqlstore.Store) *Picker {
	return &Picker{store: store}
}

// Pick returns up to `limit` tasks that are eligible for scheduling along
// with a PickDecisions struct describing why candidates were skipped.
//
// Eligible means: status=todo, manual=false, non-parent/plan kind, agent
// tasks have an agent_profile, all dependencies are done, AND the task's
// project_id is not already held by a doing task or tentatively allocated
// in this same tick (per-project max concurrency = 1 in v0.0.1; tracked
// at the scheduler layer rather than in config for now).
//
// Candidates are ordered by priority ASC, created_at ASC. The returned
// PickDecisions always has a non-nil Counts map. Counters are built on a
// local map — the picker runs on a single goroutine, so no lock is needed.
func (p *Picker) Pick(limit int) ([]sqlstore.TaskRecord, PickDecisions, error) {
	decisions := newPickDecisions()

	// Get all todo, non-manual tasks. Status=blocked rows (retry-exhausted,
	// permanent-error-blocked) never reach this call site — the picker's
	// observability is scoped to decisions over todo candidates.
	candidates, err := p.store.ListTasks(sqlstore.TaskFilter{
		Status: "todo",
		Limit:  0, // get all candidates, we filter below
	})
	if err != nil {
		return nil, decisions, err
	}
	decisions.Candidates = len(candidates)

	// Collect project_ids currently in-flight so we can enforce the per-
	// project-max-1 rule. Tasks without a project_id are not gated
	// (project-scoped concurrency only applies when a task declares a
	// project); those serialize via worker count instead.
	busy, err := p.store.ListTasks(sqlstore.TaskFilter{Status: "doing"})
	if err != nil {
		return nil, decisions, err
	}
	busyProjects := make(map[string]struct{}, len(busy))
	for _, t := range busy {
		if pk := projectKey(t); pk != "" {
			busyProjects[pk] = struct{}{}
		}
	}

	// Also track project_ids we've already tentatively allocated during THIS
	// pick pass so two same-project tasks don't both get selected in a
	// single tick.
	allocated := make(map[string]struct{})

	record := func(taskID, reason string) {
		decisions.Counts[reason]++
		decisions.Skipped = append(decisions.Skipped, SkipDecision{TaskID: taskID, Reason: reason})
	}

	var eligible []sqlstore.TaskRecord
	for _, task := range candidates {
		if task.Manual {
			record(task.ID, SkipReasonManual)
			continue
		}
		// Parent tasks are status-derived by ParentRollupTick, not executor-
		// dispatched. Skip them here so they don't consume worker slots.
		if task.Kind == "parent" {
			record(task.ID, SkipReasonParentKind)
			continue
		}
		// Plan tasks are pure coordination — phases hold phase_id-tagged
		// children that run on their own. The plan itself never dispatches.
		if task.Kind == "plan" {
			record(task.ID, SkipReasonPlanKind)
			continue
		}

		// Defense-in-depth for CW-20260418-0010: an agent task with no
		// agent_profile has no actionable CLI/provider to dispatch. Even
		// with the scheduler's pre-dispatch Validate hook in place, skip
		// these at the picker so they don't burn a tick-worth of heartbeat
		// noise or block other eligible tasks for the same project slot.
		// The task stays `todo` with its current blocked_reason; an
		// operator (or MCP update) must populate agent_profile before it
		// becomes eligible.
		if task.Kind == "agent" && task.AgentProfile == "" {
			record(task.ID, SkipReasonEmptyProfile)
			continue
		}

		// Per-project concurrency gate (no gate for project-less tasks).
		// Only consult the gate here — do not reserve the slot yet. The
		// reservation happens after all other eligibility checks pass so a
		// dep-blocked task cannot silently starve other same-project
		// siblings (CW-20260418-0003).
		pk := projectKey(task)
		if pk != "" {
			if _, inflight := busyProjects[pk]; inflight {
				record(task.ID, SkipReasonProjectBusy)
				continue
			}
			if _, taken := allocated[pk]; taken {
				record(task.ID, SkipReasonProjectContention)
				continue
			}
		}

		// Check dependencies
		if task.DependsOn.Valid && task.DependsOn.String != "" {
			var deps []string
			if err := json.Unmarshal([]byte(task.DependsOn.String), &deps); err != nil {
				record(task.ID, SkipReasonDepMalformed)
				continue // skip tasks with malformed dependencies
			}

			allMet := true
			for _, depID := range deps {
				depTask, err := p.store.GetTask(depID)
				if err != nil {
					allMet = false
					break
				}
				if depTask.Status != "done" {
					allMet = false
					break
				}
			}
			if !allMet {
				record(task.ID, SkipReasonDepUnmet)
				continue
			}
		}

		// All checks passed — now reserve the per-project slot for this tick.
		if pk != "" {
			allocated[pk] = struct{}{}
		}

		eligible = append(eligible, task)
		if len(eligible) >= limit {
			break
		}
	}

	return eligible, decisions, nil
}

// projectKey returns the concurrency key for a task. Tasks without a
// project_id share a single anonymous bucket so they serialize too.
func projectKey(t sqlstore.TaskRecord) string {
	if !t.ProjectID.Valid || t.ProjectID.String == "" {
		return ""
	}
	return t.ProjectID.String
}
