package sqlstore

import (
	"database/sql"
	"fmt"
)

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
	// CacheReadTokens / CacheWriteTokens are the run's prompt-cache totals,
	// and ProviderCost / EstimatedCost the parts of Cost the CLI reported
	// and the catalog estimated (migration 034, CW-20260912-0003).
	CacheReadTokens  int
	CacheWriteTokens int
	ProviderCost     float64
	EstimatedCost    float64
}

// AppendCostLedger inserts a single row and returns its auto-assigned ID.
func (s *Store) AppendCostLedger(rec *CostLedgerRecord) (int64, error) {
	return appendCostLedger(s.db, rec)
}

// execer is the Exec surface shared by *sql.DB and *sql.Tx.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func appendCostLedger(db execer, rec *CostLedgerRecord) (int64, error) {
	if rec == nil {
		return 0, fmt.Errorf("cost ledger record is nil")
	}
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
		(task_id, run_id, sprint_id, cost, prompt_tokens, completion_tokens, cost_source,
		 cache_read_tokens, cache_write_tokens, provider_cost, estimated_cost)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	res, err := db.Exec(
		q,
		rec.TaskID,
		rec.RunID,
		nullableString(rec.SprintID),
		rec.Cost,
		rec.PromptTokens,
		rec.CompletionTokens,
		rec.CostSource,
		rec.CacheReadTokens,
		rec.CacheWriteTokens,
		rec.ProviderCost,
		rec.EstimatedCost,
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
