package agent

import (
	"testing"

	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Usage events are per-update deltas: the run result sums them, and each
// one is forwarded as its own token event carrying the cache counts.
func TestTranslateStreamEventUsageSumsAndForwardsCache(t *testing.T) {
	var result executor.ExecutionResult
	var got []executor.ExecutionEvent
	cb := func(ev executor.ExecutionEvent) { got = append(got, ev) }

	for _, u := range []llmtypes.Usage{
		{InputTokens: 11200, OutputTokens: 61, CacheCreationTokens: 11000},
		{InputTokens: 180, OutputTokens: 48, CacheReadTokens: 11000, CacheCreationTokens: 200},
	} {
		u := u
		translateStreamEvent(llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &u}, &result, cb, nil)
	}

	assert.Equal(t, 11380, result.Tokens.PromptTokens)
	assert.Equal(t, 109, result.Tokens.CompletionTokens)
	require.Len(t, got, 2)
	assert.Equal(t, executor.TokenUsage{PromptTokens: 180, CompletionTokens: 48, CacheReadTokens: 11000, CacheWriteTokens: 200}, *got[1].Tokens)
}

// A usage event's provider cost (Usage.CostUSD, a per-event delta) sums into
// the run's Cost; an event without one leaves its tokens, cache included, in
// UnpricedTokens for the scheduler to estimate. Each event lands in exactly
// one, so nothing is priced twice (CW-20260912-0003). Here a resumed Claude
// session: its first turn reports no cost (no baseline), the next two do.
func TestTranslateStreamEventSplitsProviderCostFromUnpricedTokens(t *testing.T) {
	var result executor.ExecutionResult
	var got []executor.ExecutionEvent
	cb := func(ev executor.ExecutionEvent) { got = append(got, ev) }

	for _, u := range []llmtypes.Usage{
		{InputTokens: 12, OutputTokens: 300, CacheReadTokens: 40000, CacheCreationTokens: 5000},
		{InputTokens: 10, OutputTokens: 700, CacheReadTokens: 45000, CacheCreationTokens: 800, CostUSD: 0.05},
		{InputTokens: 14, OutputTokens: 804, CacheReadTokens: 47711, CacheCreationTokens: 14765, CostUSD: 0.09},
	} {
		u := u
		translateStreamEvent(llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &u}, &result, cb, nil)
	}

	assert.InDelta(t, 0.14, result.Cost, 1e-9, "the provider-reported part")
	assert.Equal(t, executor.TokenUsage{PromptTokens: 36, CompletionTokens: 1804, CacheReadTokens: 132711, CacheWriteTokens: 20565}, result.Tokens, "every event's tokens")
	assert.Equal(t, executor.TokenUsage{PromptTokens: 12, CompletionTokens: 300, CacheReadTokens: 40000, CacheWriteTokens: 5000}, result.UnpricedTokens, "only the turn with no cost")
	require.Len(t, got, 3)
	assert.Equal(t, 0.05, got[1].Tokens.Cost, "a token event carries its own provider cost")
	assert.Zero(t, got[0].Tokens.Cost)
}
