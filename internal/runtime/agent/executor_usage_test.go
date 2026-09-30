package agent

import (
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
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
