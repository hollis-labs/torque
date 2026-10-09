package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	runtimeevents "github.com/hollis-labs/substrate/harness/adapters/runtimeevents"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/redact"
)

const testSecret = "sk-test-0123456789abcdef"

// Only secret-named variables are secrets: a path or a model name in the
// same env stays readable in what Torque persists.
func TestLaunchRedactor_SecretNamedValuesOnly(t *testing.T) {
	r := launchRedactor(
		map[string]string{"OPENCODE_CONFIG_DIR": "/tmp/torque-boot/b1", "PLANTED_API_KEY": "planted-key-1"},
		[]string{"OPENAI_API_KEY=" + testSecret, "TORQUE_WORK_ROOT=/work/project", "HOME=/home/agent", "TORQUE_API_TOKEN=on"},
		[]string{"CLAUDE_CODE_OAUTH_TOKEN=oauth-token-xyz", "MALFORMED"},
	)
	got := r.Text("key " + testSecret + " oauth-token-xyz planted-key-1 in /work/project and /home/agent via /tmp/torque-boot/b1 on")
	assert.Equal(t, "key [redacted] [redacted] [redacted] in /work/project and /home/agent via /tmp/torque-boot/b1 on", got)
	assert.Nil(t, launchRedactor(nil, []string{"HOME=/home/agent"}), "no secrets, no redactor")
}

// The event's tool-use input is the producer's map: redacting must copy it.
func TestRedactEvent_CopiesWhatItRedacts(t *testing.T) {
	r := redact.New(testSecret)
	input := map[string]any{"command": "echo " + testSecret, "env": []any{"KEY=" + testSecret, 3.0}, "n": 1.0}
	ev := llmtypes.StreamEvent{
		Type: llmtypes.EventToolUse, Content: "c " + testSecret, Error: "e " + testSecret,
		ToolUse:       &llmtypes.ToolUseBlock{ID: "t1", Name: "bash", Input: input},
		ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: "t " + testSecret},
	}
	got := redactEvent(r, ev)
	assert.Equal(t, "c [redacted]", got.Content)
	assert.Equal(t, "e [redacted]", got.Error)
	assert.Equal(t, map[string]any{"command": "echo [redacted]", "env": []any{"KEY=[redacted]", 3.0}, "n": 1.0}, got.ToolUse.Input)
	assert.Equal(t, "t [redacted]", got.ThinkingBlock.Thinking)
	assert.Equal(t, "echo "+testSecret, input["command"], "the producer's input map was edited")
	assert.Equal(t, "t "+testSecret, ev.ThinkingBlock.Thinking, "the producer's thinking block was edited")
	assert.Equal(t, ev, redactEvent(nil, ev), "a nil redactor leaves the event alone")
}

// The legacy path copies stderr in arbitrary chunks: a secret split across
// writes must still go, and a last unterminated line is written on close.
func TestRedactStderr_SplitWritesAndClose(t *testing.T) {
	var out bytes.Buffer
	closed := false
	w, closer := redactStderr(&out, func() { closed = true }, redact.New(testSecret))
	_, _ = w.Write([]byte("first " + testSecret[:6]))
	_, _ = w.Write([]byte(testSecret[6:] + " line\nsecond " + testSecret))
	assert.Equal(t, "first [redacted] line\n", out.String(), "only whole lines are written")
	closer()
	assert.True(t, closed)
	assert.Equal(t, "first [redacted] line\nsecond [redacted]", out.String())

	var plain bytes.Buffer
	pw, _ := redactStderr(&plain, func() {}, nil)
	assert.Same(t, &plain, pw, "nothing to redact, no wrapper")
}

// After session.ready a failed turn's error reaches stream.jsonl and the
// executor's fanout (the run's Reason) through the sink's emit.
func TestWrapperSink_RedactsEventsBeforePersistingOrFanningOut(t *testing.T) {
	logDir := t.TempDir()
	fanout := make(chan llmtypes.StreamEvent, 8)
	sidecar := openStreamSidecar(logDir)
	s := &torqueRuntimeEventSink{fanout: fanout, sidecar: sidecar, redact: redact.New(testSecret)}
	raw, err := json.Marshal(map[string]any{"error": "401: key " + testSecret + " is invalid"})
	require.NoError(t, err)
	require.NoError(t, s.Write(context.Background(), runtimeevents.Event{Kind: runtimeevents.KindTurnFailed, Payload: raw}))
	sidecar.Close()
	close(fanout)
	var errs []string
	for ev := range fanout {
		errs = append(errs, ev.Error)
	}
	assert.Equal(t, []string{"401: key [redacted] is invalid"}, errs)
	stream, err := os.ReadFile(filepath.Join(logDir, "stream.jsonl"))
	require.NoError(t, err)
	assert.NotContains(t, string(stream), testSecret)
	assert.Contains(t, string(stream), "401: key [redacted] is invalid")
}

// The legacy path (codex app-server) persists and forwards events through
// startStreamFanout's drain.
func TestStartStreamFanout_Redacts(t *testing.T) {
	logDir := t.TempDir()
	downstream := make(chan llmtypes.StreamEvent, 4)
	in, closer := startStreamFanout(logDir, 4, downstream, nil, nil, redact.New(testSecret))
	in <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "token is " + testSecret}
	closer()
	got := <-downstream
	assert.Equal(t, "token is [redacted]", got.Content)
	stream, err := os.ReadFile(filepath.Join(logDir, "stream.jsonl"))
	require.NoError(t, err)
	assert.NotContains(t, string(stream), testSecret)
}
