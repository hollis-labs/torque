package scheduler

import (
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// CostSource identifies where a cost_ledger entry's `cost` value came from.
// Drives the UI's "measured" vs "estimated" badge and gives ops audit footing
// when reconciling against external billing.
type CostSource string

const (
	// CostSourceExecutor — `cost` was reported by the executor stream
	// (e.g., Claude CLI's `cost_usd` field). Authoritative.
	CostSourceExecutor CostSource = "executor"
	// CostSourceModelsDev — `cost` was computed from prompt/completion token
	// counts via the models.dev catalog because the executor didn't emit one.
	// Estimate; precision depends on catalog freshness.
	CostSourceModelsDev CostSource = "models_dev"
	// CostSourceUnknown — neither path produced a cost figure (or the row
	// pre-dates migration 017). Treat as "we don't know."
	CostSourceUnknown CostSource = "unknown"
)

// CostEntry is a single cost record for a run.
type CostEntry struct {
	TaskID           string
	RunID            int64
	SprintID         string
	Cost             float64
	PromptTokens     int
	CompletionTokens int
	Source           CostSource
}

// CostTracker records and queries execution costs.
type CostTracker struct {
	store *sqlstore.Store
}

// NewCostTracker creates a new cost tracker.
func NewCostTracker(store *sqlstore.Store) *CostTracker {
	return &CostTracker{store: store}
}

// Record persists a cost entry to the cost_ledger table. Empty Source is
// stored as 'unknown' so the column constraint is always satisfied.
func (c *CostTracker) Record(entry CostEntry) error {
	source := entry.Source
	if source == "" {
		source = CostSourceUnknown
	}
	_, err := c.store.DB().Exec(
		`INSERT INTO cost_ledger (task_id, run_id, sprint_id, cost, prompt_tokens, completion_tokens, cost_source)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.TaskID, entry.RunID, nullableString(entry.SprintID),
		entry.Cost, entry.PromptTokens, entry.CompletionTokens, string(source),
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

// EstimateCostFn matches modelcatalog.Catalog.EstimateCost. Lifted out so the
// cost policy can be tested without standing up a real catalog. Pass nil to
// disable the backfill code path.
type EstimateCostFn func(providerID, modelID string, promptTokens, completionTokens int) (float64, bool)

// ProfileResolverFn returns the (provider, model) configured for a profile.
// Used by ResolveCost to look up dispatch metadata. Pass nil to disable
// backfill (mirrors the catalog gate).
type ProfileResolverFn func(profileName string) (provider, model string, ok bool)

// ResolveCost is the package-level cost-decision function. Picks between the
// executor's reported cost and a models.dev estimate.
//
// The executor stream wins whenever it produced a positive figure. When it
// didn't (cost==0) and the call has positive tokens, we look up the dispatched
// profile's (provider, model) and ask the catalog for a price.
//
// Returns (cost, source) — source records which path produced the value so
// the UI can show "measured" vs "estimated" and ops can audit ledger
// accuracy. All branches degrade safely: cold catalog, unwired profiles,
// missing flag, or zero tokens fall through to (executor-cost, unknown).
func ResolveCost(
	profileName string,
	result *executor.ExecutionResult,
	estimate EstimateCostFn,
	resolveProfile ProfileResolverFn,
	backfillEnabled bool,
) (float64, CostSource) {
	if result == nil {
		return 0, CostSourceUnknown
	}
	if result.Cost > 0 {
		return result.Cost, CostSourceExecutor
	}

	if !backfillEnabled || estimate == nil || resolveProfile == nil {
		return result.Cost, CostSourceUnknown
	}
	if result.Tokens.PromptTokens == 0 && result.Tokens.CompletionTokens == 0 {
		return result.Cost, CostSourceUnknown
	}
	provider, model, ok := resolveProfile(profileName)
	if !ok || provider == "" || model == "" {
		return result.Cost, CostSourceUnknown
	}
	estimated, ok := estimate(provider, model,
		result.Tokens.PromptTokens, result.Tokens.CompletionTokens)
	if !ok {
		return result.Cost, CostSourceUnknown
	}
	return estimated, CostSourceModelsDev
}

// resolveCost adapts the Scheduler's instance fields into ResolveCost's
// function-typed inputs. Each closure returns nil when its source is unwired
// so the policy short-circuits cleanly. Backfill is on by default — the
// CostBackfillDisabled flag inverts the gate when set.
func (s *Scheduler) resolveCost(profileName string, result *executor.ExecutionResult) (float64, CostSource) {
	var estimate EstimateCostFn
	if s.Models != nil {
		estimate = s.Models.EstimateCost
	}
	var resolveProfile ProfileResolverFn
	if s.Profiles != nil {
		resolveProfile = func(name string) (string, string, bool) {
			p, ok := s.Profiles[name]
			if !ok {
				return "", "", false
			}
			return p.Provider, p.Model, true
		}
	}
	return ResolveCost(profileName, result, estimate, resolveProfile, !s.CostBackfillDisabled)
}
