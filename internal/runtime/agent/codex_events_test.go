package agent

import (
	"encoding/json"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/stretchr/testify/assert"
)

func TestCodexItemCompletedEvent_CommandExecution(t *testing.T) {
	params := json.RawMessage(`{"item":{"type":"commandExecution","id":"call_1","command":"/bin/zsh -lc 'echo hi'","exitCode":0},"threadId":"t","turnId":"u"}`)
	ev, ok := codexItemCompletedEvent(params)
	assert.True(t, ok)
	assert.Equal(t, llmtypes.EventToolUse, ev.Type)
	if assert.NotNil(t, ev.ToolUse) {
		// Maps to the canonical "Bash" editing-tool label so the engine-side
		// completion histogram counts codex command activity.
		assert.Equal(t, "Bash", ev.ToolUse.Name)
		assert.Equal(t, "call_1", ev.ToolUse.ID)
		assert.Equal(t, "/bin/zsh -lc 'echo hi'", ev.ToolUse.Input["command"])
	}
}

func TestCodexItemCompletedEvent_FileChange(t *testing.T) {
	params := json.RawMessage(`{"item":{"type":"fileChange","id":"fc_1"}}`)
	ev, ok := codexItemCompletedEvent(params)
	assert.True(t, ok)
	assert.Equal(t, llmtypes.EventToolUse, ev.Type)
	if assert.NotNil(t, ev.ToolUse) {
		assert.Equal(t, "Edit", ev.ToolUse.Name)
	}
}

func TestCodexItemCompletedEvent_AgentMessage(t *testing.T) {
	params := json.RawMessage(`{"item":{"type":"agentMessage","id":"msg_1","text":"working on it","phase":"commentary"}}`)
	ev, ok := codexItemCompletedEvent(params)
	assert.True(t, ok)
	assert.Equal(t, llmtypes.EventDelta, ev.Type)
	assert.Equal(t, "working on it\n", ev.Content)
}

func TestCodexItemCompletedEvent_IgnoredTypes(t *testing.T) {
	// userMessage is the input echo; empty agentMessage and unknown types
	// carry no downstream signal.
	for _, raw := range []string{
		`{"item":{"type":"userMessage","content":[{"type":"text","text":"hi"}]}}`,
		`{"item":{"type":"agentMessage","text":""}}`,
		`{"item":{"type":"reasoning"}}`,
		`not json`,
	} {
		_, ok := codexItemCompletedEvent(json.RawMessage(raw))
		assert.False(t, ok, "raw=%s", raw)
	}
}

func TestCodexTokenUsageTotals(t *testing.T) {
	params := json.RawMessage(`{"tokenUsage":{"total":{"totalTokens":16445,"inputTokens":16370,"cachedInputTokens":3456,"outputTokens":75}},"threadId":"t"}`)
	in, out, cache, ok := codexTokenUsageTotals(params)
	assert.True(t, ok)
	assert.Equal(t, 16370, in)
	assert.Equal(t, 75, out)
	assert.Equal(t, 3456, cache)
}

func TestCodexTokenUsageTotals_EmptyOrBad(t *testing.T) {
	for _, raw := range []string{
		`{"tokenUsage":{"total":{"inputTokens":0,"outputTokens":0}}}`,
		`{}`,
		`garbage`,
	} {
		_, _, _, ok := codexTokenUsageTotals(json.RawMessage(raw))
		assert.False(t, ok, "raw=%s", raw)
	}
}

// TestCodexTokenUsageDeltaSemantics documents the cumulative→delta contract
// the boot.go hook relies on: codex reports running totals, and
// translateStreamEvent SUMS InputTokens/OutputTokens, so the hook emits the
// difference between successive totals. Summing the per-update deltas must
// reconstruct the final cumulative total.
func TestCodexTokenUsageDeltaSemantics(t *testing.T) {
	updates := []string{
		`{"tokenUsage":{"total":{"inputTokens":5000,"outputTokens":20}}}`,
		`{"tokenUsage":{"total":{"inputTokens":11000,"outputTokens":50}}}`,
		`{"tokenUsage":{"total":{"inputTokens":16370,"outputTokens":75}}}`,
	}
	var prevIn, prevOut int
	var sumIn, sumOut int
	for _, raw := range updates {
		in, out, _, ok := codexTokenUsageTotals(json.RawMessage(raw))
		assert.True(t, ok)
		dIn, dOut := in-prevIn, out-prevOut
		prevIn, prevOut = in, out
		sumIn += dIn
		sumOut += dOut
	}
	assert.Equal(t, 16370, sumIn, "summed input deltas reconstruct the final total")
	assert.Equal(t, 75, sumOut, "summed output deltas reconstruct the final total")
}
