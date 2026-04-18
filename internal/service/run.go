package service

import (
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

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

// ListFiltered returns runs matching the filter. Used by aggregate
// endpoints (Ops dashboard widgets) that need to query across tasks.
func (s *RunService) ListFiltered(f sqlstore.RunFilter) ([]sqlstore.RunRecord, error) {
	return s.store.ListRunsFiltered(f)
}

// Complete marks a run as finished with the provided completion data.
func (s *RunService) Complete(id int64, c sqlstore.RunCompletion) error {
	return s.store.CompleteRun(id, c)
}

// Aggregate returns the per-task run count / token / cost roll-up.
func (s *RunService) Aggregate(taskID string) (*sqlstore.TaskRunAggregate, error) {
	return s.store.GetTaskRunAggregate(taskID)
}

// Cancel stamps a run as operator-cancelled with the caller-supplied reason
// written to error_message. Already-terminal runs (done/failed/blocked/review/
// cancelled/superseded/killed) are rejected so a stale cancel cannot rewrite
// completed history. Intended for operator-facing surfaces (HTTP/MCP) — the
// scheduler lifecycle does not call this path.
func (s *RunService) Cancel(id int64, reason string) error {
	return s.transitionOperator(id, sqlstore.RunStatusCancelled, reason)
}

// Kill stamps a run as scheduler/operator-killed (live-validation cleanup,
// shutdown, stale-worker cull). Same in-flight guard as Cancel.
func (s *RunService) Kill(id int64, reason string) error {
	return s.transitionOperator(id, sqlstore.RunStatusKilled, reason)
}

// Supersede stamps a run as superseded with a caller-supplied reason
// (typically citing the accepting run/commit). Same in-flight guard as
// Cancel. Distinct from the lifecycle-internal markRunSuperseded path
// (late-arriving result on a terminal task) — this is the operator-driven
// formal supersede.
func (s *RunService) Supersede(id int64, reason string) error {
	return s.transitionOperator(id, sqlstore.RunStatusSuperseded, reason)
}

// transitionOperator is the shared guard+write for operator-terminal
// transitions. Refuses to transition a run that is no longer running —
// cancels of already-finished runs are a no-op-with-error so callers see
// the race and the original outcome is preserved.
func (s *RunService) transitionOperator(id int64, status, reason string) error {
	cur, err := s.store.GetRun(id)
	if err != nil {
		return err
	}
	if cur.Status != sqlstore.RunStatusRunning {
		return fmt.Errorf("run %d is already terminal (%s); cannot transition to %s", id, cur.Status, status)
	}
	return s.store.SetRunOperatorStatus(id, status, reason)
}
