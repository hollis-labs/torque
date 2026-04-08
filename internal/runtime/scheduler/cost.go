package scheduler

import (
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// CostEntry is a single cost record for a run.
type CostEntry struct {
	TaskID           string
	RunID            int64
	SprintID         string
	Cost             float64
	PromptTokens     int
	CompletionTokens int
}

// CostTracker records and queries execution costs.
type CostTracker struct {
	store *sqlstore.Store
}

// NewCostTracker creates a new cost tracker.
func NewCostTracker(store *sqlstore.Store) *CostTracker {
	return &CostTracker{store: store}
}

// Record persists a cost entry to the cost_ledger table.
func (c *CostTracker) Record(entry CostEntry) error {
	_, err := c.store.DB().Exec(
		`INSERT INTO cost_ledger (task_id, run_id, sprint_id, cost, prompt_tokens, completion_tokens)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		entry.TaskID, entry.RunID, nullableString(entry.SprintID),
		entry.Cost, entry.PromptTokens, entry.CompletionTokens,
	)
	return err
}

// TaskTotal returns the total cost for a task across all runs.
func (c *CostTracker) TaskTotal(taskID string) (float64, error) {
	var total float64
	err := c.store.DB().QueryRow(
		`SELECT COALESCE(SUM(cost), 0) FROM cost_ledger WHERE task_id = ?`, taskID,
	).Scan(&total)
	return total, err
}

// SprintTotal returns the total cost for a sprint across all tasks.
func (c *CostTracker) SprintTotal(sprintID string) (float64, error) {
	var total float64
	err := c.store.DB().QueryRow(
		`SELECT COALESCE(SUM(cost), 0) FROM cost_ledger WHERE sprint_id = ?`, sprintID,
	).Scan(&total)
	return total, err
}

// GlobalTotal returns the total cost across all tasks and sprints.
func (c *CostTracker) GlobalTotal() (float64, error) {
	var total float64
	err := c.store.DB().QueryRow(
		`SELECT COALESCE(SUM(cost), 0) FROM cost_ledger`,
	).Scan(&total)
	return total, err
}

// WithinGlobalBudget returns true if the global total is under the ceiling.
// A ceiling of 0 means no limit.
func (c *CostTracker) WithinGlobalBudget(ceiling float64) (bool, error) {
	if ceiling <= 0 {
		return true, nil
	}
	total, err := c.GlobalTotal()
	if err != nil {
		return false, err
	}
	return total < ceiling, nil
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
