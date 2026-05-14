package waitpoll

import (
	"context"
	"fmt"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// TaskDone is a predicate that fires when a target task reaches the "done"
// status. params: {"task_id": "<id>"}.
type TaskDone struct {
	store *sqlstore.Store
}

// NewTaskDone constructs a TaskDone predicate backed by the given store.
func NewTaskDone(s *sqlstore.Store) *TaskDone { return &TaskDone{store: s} }

func (p *TaskDone) Type() string { return "task_done" }

func (p *TaskDone) Validate(params map[string]any) error {
	id, _ := params["task_id"].(string)
	if id == "" {
		return fmt.Errorf("task_done: params.task_id required")
	}
	return nil
}

// Evaluate returns (true, nil) when the target task's status is "done".
// A missing target task returns the underlying store error so callers can
// distinguish a typo in the wait-task's config from a legitimate not-yet.
func (p *TaskDone) Evaluate(ctx context.Context, params map[string]any) (bool, error) {
	id, _ := params["task_id"].(string)
	t, err := p.store.GetTask(id)
	if err != nil {
		return false, err
	}
	return t.Status == "done", nil
}
