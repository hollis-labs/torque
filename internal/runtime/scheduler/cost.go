package scheduler

import (
	"context"
	"log"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/launchprofile"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/writequeue"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// CostSource records where a run's cost came from, on runs.cost_source and
// its cost_ledger row. It drives the UI's measured-vs-estimated badge and
// tells an agent reading total_cost which part is an estimate.
type CostSource string

// Provenance since migration 034 (CW-20260912-0003). One set, lead's
// provider | estimate | none plus mixed: a run can be both, as when a
// resumed Claude session's first turn reports no cost and is estimated
// while its other turns carry Claude's own figure, and calling that run
// either "provider" or "estimate" would misstate part of its cost.
const (
	// CostSourceProvider: every priced token came with the CLI's own cost
	// figure (Usage.CostUSD: Claude's total_cost_usd per turn, opencode's
	// per step). The CLI's list-price computation, not an invoice.
	CostSourceProvider CostSource = "provider"
	// CostSourceEstimate: priced from the models.dev catalog, cache-aware.
	CostSourceEstimate CostSource = "estimate"
	// CostSourceMixed: part provider-reported, part estimated.
	CostSourceMixed CostSource = "mixed"
	// CostSourceNone: no figure; no provider cost, and the tokens (if
	// any) could not be priced (unknown model, cold catalog, backfill off).
	// The 0 it carries means unknown, not free.
	CostSourceNone CostSource = "none"
)

// Ledger values written before migration 034, still read: executor is
// provider, models_dev is a (cache-unaware) estimate, unknown is none.
const (
	CostSourceExecutor  CostSource = "executor"
	CostSourceModelsDev CostSource = "models_dev"
	CostSourceUnknown   CostSource = "unknown"
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
	store  *sqlstore.Store
	writer writequeue.TelemetryWriter
}

// NewCostTracker creates a new cost tracker.
func NewCostTracker(store *sqlstore.Store) *CostTracker {
	return &CostTracker{store: store, writer: writequeue.NewDirect(store)}
}

// SetTelemetryWriter installs the sink used for cost_ledger rows. Nil keeps
// the existing writer.
func (c *CostTracker) SetTelemetryWriter(w writequeue.TelemetryWriter) {
	if w == nil {
		return
	}
	c.writer = w
}

// Record persists a cost entry to the cost_ledger table. Empty Source is
// stored as 'unknown' so the column constraint is always satisfied.
func (c *CostTracker) Record(entry CostEntry) error {
	source := entry.Source
	if source == "" {
		source = CostSourceUnknown
	}
	return c.writer.RecordCost(context.Background(), &sqlstore.CostLedgerRecord{
		TaskID:           entry.TaskID,
		RunID:            entry.RunID,
		SprintID:         entry.SprintID,
		Cost:             entry.Cost,
		PromptTokens:     entry.PromptTokens,
		CompletionTokens: entry.CompletionTokens,
		CostSource:       string(source),
	})
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

// TotalsBySource returns the ledger's cost totals keyed by cost_source. They
// sum to GlobalTotal.
func (c *CostTracker) TotalsBySource() (map[string]float64, error) {
	rows, err := c.store.ReadDB().Query(`SELECT cost_source, COALESCE(SUM(cost), 0) FROM cost_ledger GROUP BY cost_source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var source string
		var total float64
		if err := rows.Scan(&source, &total); err != nil {
			return nil, err
		}
		out[source] = total
	}
	return out, rows.Err()
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

// EstimateUsageFn matches modelcatalog.Catalog.EstimateUsageCost: a
// cache-aware price for a run's tokens. Lifted out so the cost policy can be
// tested without a real catalog. Pass nil to disable estimating.
type EstimateUsageFn func(providerID, modelID string, u modelcatalog.UsageTokens) (cost float64, cacheFallback bool, ok bool)

// TaskProfile is the pair of profile fields a task launches with. The
// executor resolves them together, launch_profile first
// (launchprofile.Resolve), so cost resolution must too: a task with only a
// launch_profile has no agent_profile to look up.
type TaskProfile struct {
	LaunchProfile string
	AgentProfile  string
}

// ProfileResolverFn returns the provider (the profile's runtime, as
// configured) and model a task's profile resolves to, the way the executor
// resolves it. Pass nil to disable estimating.
type ProfileResolverFn func(profile TaskProfile) (provider, model string, ok bool)

// ResolvedCost is a run's cost and where it came from. Cost is
// ProviderCost + EstimatedCost.
type ResolvedCost struct {
	Cost          float64
	ProviderCost  float64
	EstimatedCost float64
	Source        CostSource
	// CacheFallback reports that a cache price was missing from the
	// catalog and the input price stood in for it.
	CacheFallback bool
}

// ResolveCost decides a run's cost once, provider first (CW-20260912-0003).
//
// result.Cost is what the CLI reported for the usage events that carried a
// cost; result.UnpricedTokens are the tokens of the events that did not.
// Those are priced from the catalog, cache-aware: cache reads and writes at
// their own prices, and input counted per the runtime's convention (codex
// input includes cache reads; Claude Code's and OpenCode's do not). An
// executor that does not split leaves UnpricedTokens zero, and then all of
// result.Tokens is priced when it reported no cost.
//
// Every branch degrades safely: a cold catalog, an unknown profile or model,
// or backfill turned off leave the unpriced tokens unpriced, and a run with
// no figure at all is CostSourceNone.
func ResolveCost(
	profile TaskProfile,
	result *executor.ExecutionResult,
	estimate EstimateUsageFn,
	resolveProfile ProfileResolverFn,
	backfillEnabled bool,
) ResolvedCost {
	if result == nil {
		return ResolvedCost{Source: CostSourceNone}
	}
	rc := ResolvedCost{ProviderCost: max(result.Cost, 0)}
	unpriced := result.UnpricedTokens
	if !hasTokens(unpriced) && rc.ProviderCost == 0 {
		unpriced = result.Tokens
	}
	estimated := false
	if hasTokens(unpriced) && backfillEnabled && estimate != nil && resolveProfile != nil {
		if provider, model, ok := resolveProfile(profile); ok && provider != "" && model != "" {
			cost, fallback, ok := estimate(config.CatalogProviderID(provider), model, modelcatalog.UsageTokens{
				Input:                  unpriced.PromptTokens,
				Output:                 unpriced.CompletionTokens,
				CacheRead:              unpriced.CacheReadTokens,
				CacheWrite:             unpriced.CacheWriteTokens,
				InputIncludesCacheRead: config.UsageInputIncludesCacheRead(provider),
			})
			if ok {
				rc.EstimatedCost, rc.CacheFallback, estimated = cost, fallback, true
			}
		}
	}
	rc.Cost = rc.ProviderCost + rc.EstimatedCost
	switch {
	case rc.ProviderCost > 0 && estimated:
		rc.Source = CostSourceMixed
	case rc.ProviderCost > 0:
		rc.Source = CostSourceProvider
	case estimated:
		rc.Source = CostSourceEstimate
	default:
		rc.Source = CostSourceNone
	}
	return rc
}

func hasTokens(t executor.TokenUsage) bool {
	return t.PromptTokens > 0 || t.CompletionTokens > 0 || t.CacheReadTokens > 0 || t.CacheWriteTokens > 0
}

// resolveCost adapts the Scheduler's instance fields into ResolveCost's
// function-typed inputs. Each closure is nil when its source is unwired so
// the policy short-circuits cleanly. Backfill is on by default; the
// CostBackfillDisabled flag turns estimating off. The task's profile resolves
// exactly as the executor resolves it, launch_profile first, so a task
// launched by launch_profile alone is priced against the model that ran.
func (s *Scheduler) resolveCost(task *sqlstore.TaskRecord, result *executor.ExecutionResult) ResolvedCost {
	var estimate EstimateUsageFn
	if s.Models != nil {
		estimate = s.Models.EstimateUsageCost
	}
	resolveProfile := func(tp TaskProfile) (string, string, bool) {
		p := launchprofile.Resolve(launchprofile.ResolveRequest{
			LaunchProfile:      tp.LaunchProfile,
			LegacyAgentProfile: tp.AgentProfile,
			Source:             s.Profiles,
		}).AgentProfile
		return p.Provider, p.Model, true
	}
	profile := TaskProfile{LaunchProfile: task.LaunchProfile, AgentProfile: task.AgentProfile}
	rc := ResolveCost(profile, result, estimate, resolveProfile, !s.CostBackfillDisabled)
	if rc.CacheFallback {
		log.Printf("[scheduler] cost for task %s (launch_profile %q, agent_profile %q) priced a cache read or write at the input price: the catalog has no cache price for its model", task.ID, profile.LaunchProfile, profile.AgentProfile)
	}
	return rc
}

// hasUsage reports whether an executor result carries any usage or cost worth
// recording: tokens of any kind, or a provider-reported cost.
func hasUsage(result *executor.ExecutionResult) bool {
	return result != nil && (hasTokens(result.Tokens) || hasTokens(result.UnpricedTokens) || result.Cost > 0)
}

// usageOrNil returns result when it carries usage or cost to record, and nil
// otherwise, so a run that ended without any writes no cost fields.
func usageOrNil(result *executor.ExecutionResult) *executor.ExecutionResult {
	if hasUsage(result) {
		return result
	}
	return nil
}

// costWrite builds what a run's completion row and its cost_ledger row carry
// for result, priced once by resolveCost. Every path that ends a run writes
// them through here, so a run completed, failed, cancelled or interrupted with
// usage records the same fields the same way. The ledger record is nil when
// result is nil.
func (s *Scheduler) costWrite(task *sqlstore.TaskRecord, result *executor.ExecutionResult, status, reason string) (sqlstore.RunCompletion, *sqlstore.CostLedgerRecord, ResolvedCost) {
	comp := sqlstore.RunCompletion{Status: status, ErrorMessage: reason}
	if result == nil {
		return comp, nil, ResolvedCost{Source: CostSourceNone}
	}
	cost := s.resolveCost(task, result)
	comp.PromptTokens = result.Tokens.PromptTokens
	comp.CompletionTokens = result.Tokens.CompletionTokens
	comp.CacheReadTokens = result.Tokens.CacheReadTokens
	comp.CacheWriteTokens = result.Tokens.CacheWriteTokens
	comp.Cost = cost.Cost
	comp.CostSource = string(cost.Source)
	comp.ExitCode = result.ExitCode
	sprintID := ""
	if task.SprintID.Valid {
		sprintID = task.SprintID.String
	}
	return comp, &sqlstore.CostLedgerRecord{
		TaskID:           task.ID,
		SprintID:         sprintID,
		PromptTokens:     result.Tokens.PromptTokens,
		CompletionTokens: result.Tokens.CompletionTokens,
		CacheReadTokens:  result.Tokens.CacheReadTokens,
		CacheWriteTokens: result.Tokens.CacheWriteTokens,
		ProviderCost:     cost.ProviderCost,
		EstimatedCost:    cost.EstimatedCost,
	}, cost
}
