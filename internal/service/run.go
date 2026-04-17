package service

import "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"

// RunService provides business logic for task runs.
type RunService struct {
	store *sqlstore.Store
}

// Create inserts a new run for the given task and executor, returning the new run ID.
func (s *RunService) Create(taskID, executor string) (int64, error) {
	rec := &sqlstore.RunRecord{
		TaskID:   taskID,
		Executor: executor,
	}
	return s.store.CreateRun(rec)
}

// Get fetches a run by ID.
func (s *RunService) Get(id int64) (*sqlstore.RunRecord, error) {
	return s.store.GetRun(id)
}

// List returns all runs for a task.
func (s *RunService) List(taskID string) ([]sqlstore.RunRecord, error) {
	return s.store.ListRuns(taskID)
}

// Complete marks a run as finished with the provided completion data.
func (s *RunService) Complete(id int64, c sqlstore.RunCompletion) error {
	return s.store.CompleteRun(id, c)
}

// Aggregate returns the per-task run count / token / cost roll-up.
func (s *RunService) Aggregate(taskID string) (*sqlstore.TaskRunAggregate, error) {
	return s.store.GetTaskRunAggregate(taskID)
}
