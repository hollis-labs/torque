package scheduler

import (
	"sync"

	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// runTokenTotals keeps a running sum of every token-usage event per run, so
// the tokens-class run.progress payload can carry the run's cumulative usage.
//
// Usage events are per-update deltas on every executor path (claude/codex
// once per turn, OpenCode once per step), and progressThrottler drops the
// SSE emission for any event inside its window. Summing here, before the
// throttle, is what keeps a dropped emission from dropping its tokens: the
// next emission that does go out carries them in its totals.
type runTokenTotals struct {
	mu sync.Mutex
	m  map[int64]executor.TokenUsage
}

func newRunTokenTotals() *runTokenTotals {
	return &runTokenTotals{m: make(map[int64]executor.TokenUsage)}
}

// add folds u into runID's running total and returns the new total.
func (r *runTokenTotals) add(runID int64, u executor.TokenUsage) executor.TokenUsage {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.m[runID]
	t.PromptTokens += u.PromptTokens
	t.CompletionTokens += u.CompletionTokens
	t.CacheReadTokens += u.CacheReadTokens
	t.CacheWriteTokens += u.CacheWriteTokens
	t.Cost += u.Cost
	r.m[runID] = t
	return t
}

// release drops runID's total. Call when a run ends, however it ends, so
// the map doesn't grow unbounded over long-lived schedulers.
func (r *runTokenTotals) release(runID int64) {
	r.mu.Lock()
	delete(r.m, runID)
	r.mu.Unlock()
}

// size reports how many runs are tracked. Tests use it to prove release.
func (r *runTokenTotals) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.m)
}
