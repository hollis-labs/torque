package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/redact"
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

func TestCodexTurnCompletedFailure_NestedFailedPayload(t *testing.T) {
	params := json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-1","items":[],"status":"failed","error":{"message":"unexpected status 401: missing auth","codexErrorInfo":"other","additionalDetails":null},"startedAt":"2026-09-12T00:03:00Z","completedAt":"2026-09-12T00:03:34Z"}}`)
	msg, ok := codexTurnCompletedFailure(params, nil)
	require.True(t, ok)
	assert.Equal(t, "codex terminal turn failed: unexpected status 401: missing auth", msg)
	assert.NotContains(t, msg, "codexErrorInfo")
}

func TestCodexTurnCompletedFailure_IgnoresSuccessfulOrMalformedPayloads(t *testing.T) {
	for _, raw := range []string{
		`{"turn":{"status":"completed"}}`,
		`{"status":"failed","errorMessage":"top-level legacy shape"}`,
		`not-json`,
	} {
		_, ok := codexTurnCompletedFailure(json.RawMessage(raw), nil)
		assert.False(t, ok, "raw=%s", raw)
	}
}

// The reason is redacted before it is cut to its bound, so a secret
// straddling the cut keeps no prefix, and the cut lands on a rune boundary.
func TestCodexTurnCompletedFailure_RedactsBeforeCutting(t *testing.T) {
	const secret = "sk-straddle-secret-0123456789"
	failed := func(message string) json.RawMessage {
		b, err := json.Marshal(map[string]any{"turn": map[string]any{"status": "failed", "error": map[string]any{"message": message}}})
		require.NoError(t, err)
		return b
	}
	msg, ok := codexTurnCompletedFailure(failed(strings.Repeat("x", 230)+secret), redact.New(secret))
	require.True(t, ok)
	assert.NotContains(t, msg, "sk-strad")

	msg, ok = codexTurnCompletedFailure(failed(strings.Repeat("x", 239)+"é and more"), nil)
	require.True(t, ok)
	assert.True(t, utf8.ValidString(msg), "the cut never splits a rune")
	assert.Equal(t, codexTerminalFailurePrefix+": "+strings.Repeat("x", 239)+"...", msg)
}
