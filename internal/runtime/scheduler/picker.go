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
// Eligible means: status=todo, manual=false, all dependencies are done.
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

	var eligible []sqlstore.TaskRecord
	for _, task := range candidates {
		if task.Manual {
			continue
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
