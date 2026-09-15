package agent

import (
	"encoding/json"
	"strings"

	"github.com/hollis-labs/agentkit/agentruntime/turn"
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
// The notification *parsing* (wire-shape unmarshal + cumulative→delta math)
// now lives upstream in go-agent-runtime/turn (v0.5.0): turn.ParseCodexItemCompleted,
// turn.ParseCodexTokenUsageTotals, turn.CodexTokenUsageDelta, and the
// turn.Codex* method-name constants. Torque keeps only the app-specific
// *projection* of the parsed item onto llmtypes.StreamEvent (the tool-name
// mapping below) — which the upstream README explicitly leaves to apps.

const codexTerminalFailurePrefix = "codex terminal turn failed"

// codexItemCompletedEvent projects a parsed codex `item/completed`
// notification (via turn.ParseCodexItemCompleted) onto a StreamEvent.
// Returns ok=false for item types that carry no downstream-relevant signal
// (userMessage input echoes, empty assistant text, unknown types, malformed
// JSON) — all dropped by the upstream parser.
//
// Tool-name mapping is deliberate: commandExecution → "Bash" and
// fileChange → "Edit" reuse the canonical editing-tool labels the
// engine-side verifier's editingToolNames recognizes (Edit/Write/Bash/…),
// so codex tool activity registers in the completion histogram the same
// way claude/opencode activity does.
func codexItemCompletedEvent(params json.RawMessage) (llmtypes.StreamEvent, bool) {
	item, ok := turn.ParseCodexItemCompleted(params)
	if !ok {
		return llmtypes.StreamEvent{}, false
	}
	switch item.Type {
	case "commandExecution":
		return llmtypes.StreamEvent{
			Type: llmtypes.EventToolUse,
			ToolUse: &llmtypes.ToolUseBlock{
				ID:    item.ID,
				Name:  "Bash",
				Input: map[string]any{"command": item.Command},
			},
		}, true
	case "fileChange":
		return llmtypes.StreamEvent{
			Type: llmtypes.EventToolUse,
			ToolUse: &llmtypes.ToolUseBlock{
				ID:   item.ID,
				Name: "Edit",
			},
		}, true
	case "agentMessage":
		// Non-empty assistant text (the parser already dropped the empty
		// case) — surfaced as a delta so it lands in the transcript log and
		// clears the last_activity gate. Not a tool, so it doesn't affect
		// the histogram.
		return llmtypes.StreamEvent{
			Type:    llmtypes.EventDelta,
			Content: item.Text + "\n",
		}, true
	default:
		return llmtypes.StreamEvent{}, false
	}
}

func codexTurnCompletedFailure(params json.RawMessage) (string, bool) {
	var payload struct {
		Turn struct {
			Status string `json:"status"`
			Error  struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return "", false
	}
	if !strings.EqualFold(payload.Turn.Status, "failed") {
		return "", false
	}
	reason := strings.TrimSpace(payload.Turn.Error.Message)
	if reason == "" {
		reason = "status=failed"
	}
	const maxReason = 240
	if len(reason) > maxReason {
		reason = reason[:maxReason] + "..."
	}
	return codexTerminalFailurePrefix + ": " + reason, true
}
