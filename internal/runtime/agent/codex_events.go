package agent

import (
	"encoding/json"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// codex_events.go projects codex app-server JSON-RPC notifications into
// llmtypes.StreamEvent values so the codex (jsonrpc-stdio) runtime feeds
// the same downstream pipeline the streaming-stdio/subprocess runtimes do:
// the stream sidecar (stream.jsonl → engine-side completion histogram),
// the cost accumulator (translateStreamEvent → run token totals), and the
// last_activity liveness gate.
//
// Before this, codex's adapter ParseLine returned nil (codex speaks
// JSON-RPC, not stream-json on stdout) and only `turn/completed` was
// consumed (turn-complete detection). Every other signal — tool calls,
// token usage, assistant text — was dropped, so codex runs recorded
// $0 / 0 tokens, an empty tool histogram (false "no action" blocks), and
// no liveness heartbeat. CW-20260521-0024.
//
// Wire shapes (codex-cli 0.132, captured empirically):
//
//	item/completed:
//	  {"item":{"type":"commandExecution","id":"call_…","command":"…",
//	           "exitCode":0,…}, "threadId":…, "turnId":…}
//	  {"item":{"type":"agentMessage","id":"msg_…","text":"…",
//	           "phase":"commentary"}, …}
//	  {"item":{"type":"userMessage",…}, …}            (input echo; ignored)
//	  {"item":{"type":"fileChange",…}, …}             (defensive; → Edit)
//
//	thread/tokenUsage/updated:
//	  {"tokenUsage":{"total":{"inputTokens":N,"outputTokens":N,
//	                          "cachedInputTokens":N,…}, "last":{…}}}
//	  total is CUMULATIVE over the thread — callers must delta it before
//	  feeding translateStreamEvent (which SUMS InputTokens/OutputTokens).

// codexItemCompletedEvent maps a codex `item/completed` notification's
// params onto a StreamEvent. Returns ok=false for item types that carry no
// downstream-relevant signal (userMessage input echoes, unknown types).
//
// Tool-name mapping is deliberate: commandExecution → "Bash" and
// fileChange → "Edit" reuse the canonical editing-tool labels the
// engine-side verifier's editingToolNames recognizes (Edit/Write/Bash/…),
// so codex tool activity registers in the completion histogram the same
// way claude/opencode activity does.
func codexItemCompletedEvent(params json.RawMessage) (llmtypes.StreamEvent, bool) {
	var p struct {
		Item struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Command string `json:"command"`
			Text    string `json:"text"`
		} `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return llmtypes.StreamEvent{}, false
	}
	switch p.Item.Type {
	case "commandExecution":
		return llmtypes.StreamEvent{
			Type: llmtypes.EventToolUse,
			ToolUse: &llmtypes.ToolUseBlock{
				ID:    p.Item.ID,
				Name:  "Bash",
				Input: map[string]any{"command": p.Item.Command},
			},
		}, true
	case "fileChange":
		return llmtypes.StreamEvent{
			Type: llmtypes.EventToolUse,
			ToolUse: &llmtypes.ToolUseBlock{
				ID:   p.Item.ID,
				Name: "Edit",
			},
		}, true
	case "agentMessage":
		if p.Item.Text == "" {
			return llmtypes.StreamEvent{}, false
		}
		// Assistant text — surfaced as a delta so it lands in the
		// transcript log and clears the last_activity gate. Not a tool,
		// so it doesn't affect the histogram.
		return llmtypes.StreamEvent{
			Type:    llmtypes.EventDelta,
			Content: p.Item.Text + "\n",
		}, true
	default:
		// userMessage (input echo), reasoning, and any future/unknown
		// item types carry no downstream signal worth projecting.
		return llmtypes.StreamEvent{}, false
	}
}

// codexTokenUsageTotals extracts the CUMULATIVE token counts from a
// `thread/tokenUsage/updated` notification. The caller must delta these
// against the previous cumulative values before emitting an EventUsage,
// because translateStreamEvent sums InputTokens/OutputTokens and codex
// reports running totals (not per-update increments).
//
// inputTokens is codex's full prompt count (cachedInputTokens is the
// cached subset, surfaced separately as CacheReadTokens for forensics; it
// is NOT subtracted here so PromptTokens reflects the billed prompt size).
func codexTokenUsageTotals(params json.RawMessage) (input, output, cacheRead int, ok bool) {
	var p struct {
		TokenUsage struct {
			Total struct {
				InputTokens       int `json:"inputTokens"`
				OutputTokens      int `json:"outputTokens"`
				CachedInputTokens int `json:"cachedInputTokens"`
			} `json:"total"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return 0, 0, 0, false
	}
	t := p.TokenUsage.Total
	if t.InputTokens == 0 && t.OutputTokens == 0 {
		return 0, 0, 0, false
	}
	return t.InputTokens, t.OutputTokens, t.CachedInputTokens, true
}
