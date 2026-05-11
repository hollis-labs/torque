package sqlstore

import "fmt"

// CostLedgerRecord mirrors a row in cost_ledger.
type CostLedgerRecord struct {
	ID               int64
	TaskID           string
	RunID            int64
	SprintID         string
	Cost             float64
	PromptTokens     int
	CompletionTokens int
	CostSource       string
}

// AppendCostLedger inserts a single row and returns its auto-assigned ID.
func (s *Store) AppendCostLedger(rec *CostLedgerRecord) (int64, error) {
	if rec.TaskID == "" {
		return 0, fmt.Errorf("cost ledger missing task_id")
	}
	if rec.RunID <= 0 {
		return 0, fmt.Errorf("cost ledger missing run_id")
	}
	if rec.CostSource == "" {
		rec.CostSource = "unknown"
	}

	const q = `INSERT INTO cost_ledger
		(task_id, run_id, sprint_id, cost, prompt_tokens, completion_tokens, cost_source)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	res, err := s.db.Exec(
		q,
		rec.TaskID,
		rec.RunID,
		nullableString(rec.SprintID),
		rec.Cost,
		rec.PromptTokens,
		rec.CompletionTokens,
		rec.CostSource,
	)
	if err != nil {
		return 0, fmt.Errorf("append cost ledger: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	rec.ID = id
	return id, nil
}
