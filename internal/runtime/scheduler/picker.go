package scheduler

import (
	"encoding/json"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// Picker selects tasks eligible for scheduling.
type Picker struct {
	store *sqlstore.Store
}

// NewPicker creates a new task picker.
func NewPicker(store *sqlstore.Store) *Picker {
	return &Picker{store: store}
}

// Pick returns up to `limit` tasks that are eligible for scheduling.
// Eligible means: status=todo, manual=false, all dependencies are done,
// AND the task's project_id is not already held by a doing task (per-project
// max concurrency = 1 in v0.0.1; tracked at the scheduler layer rather than
// in config for now).
// Results are ordered by priority ASC, created_at ASC.
func (p *Picker) Pick(limit int) ([]sqlstore.TaskRecord, error) {
	// Get all todo, non-manual tasks
	candidates, err := p.store.ListTasks(sqlstore.TaskFilter{
		Status: "todo",
		Limit:  0, // get all candidates, we filter below
	})
	if err != nil {
		return nil, err
	}

	// Collect project_ids currently in-flight so we can enforce the per-
	// project-max-1 rule. Tasks without a project_id are not gated
	// (project-scoped concurrency only applies when a task declares a
	// project); those serialize via worker count instead.
	busy, err := p.store.ListTasks(sqlstore.TaskFilter{Status: "doing"})
	if err != nil {
		return nil, err
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

	var eligible []sqlstore.TaskRecord
	for _, task := range candidates {
		if task.Manual {
			continue
		}
		// Parent tasks are status-derived by ParentRollupTick, not executor-
		// dispatched. Skip them here so they don't consume worker slots.
		if task.Kind == "parent" {
			continue
		}

		// Per-project concurrency gate (no gate for project-less tasks).
		if pk := projectKey(task); pk != "" {
			if _, inflight := busyProjects[pk]; inflight {
				continue
			}
			if _, taken := allocated[pk]; taken {
				continue
			}
			allocated[pk] = struct{}{}
		}

		// Check dependencies
		if task.DependsOn.Valid && task.DependsOn.String != "" {
			var deps []string
			if err := json.Unmarshal([]byte(task.DependsOn.String), &deps); err != nil {
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
				continue
			}
		}

		eligible = append(eligible, task)
		if len(eligible) >= limit {
			break
		}
	}

	return eligible, nil
}

// projectKey returns the concurrency key for a task. Tasks without a
// project_id share a single anonymous bucket so they serialize too.
func projectKey(t sqlstore.TaskRecord) string {
	if !t.ProjectID.Valid || t.ProjectID.String == "" {
		return ""
	}
	return t.ProjectID.String
}
