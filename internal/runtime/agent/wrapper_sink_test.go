package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	llmtypes "github.com/hollis-labs/go-llm-types"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/redact"
)

// sinkEvents runs one runtime event through a bare torqueRuntimeEventSink and
// returns the llmtypes.StreamEvents it fanned out, plus how often onDone fired.
func sinkEvents(t *testing.T, kind runtimeevents.EventKind, payload any) ([]llmtypes.StreamEvent, int) {
	t.Helper()
	fanout := make(chan llmtypes.StreamEvent, 8)
	done := 0
	s := &torqueRuntimeEventSink{fanout: fanout, onDone: func() { done++ }}
	ev := runtimeevents.Event{Kind: kind}
	if payload != nil {
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		ev.Payload = raw
	}
	require.NoError(t, s.Write(context.Background(), ev))
	close(fanout)
	var out []llmtypes.StreamEvent
	for e := range fanout {
		out = append(out, e)
	}
	return out, done
}

// go-agent-wrapper v0.13.1 puts a turn's summed usage on its one terminal
// event instead of a separate usage-only turn.completed. The sink must still
// produce the usage AND the EventDone turn boundary; dropping the done hangs
// ModeOneShot (onDone) and starves the reminder pump.
func TestWrapperSink_TurnCompletedWithUsage_EmitsUsageThenDone(t *testing.T) {
	usage := &llmtypes.Usage{InputTokens: 10, OutputTokens: 5}
	got, done := sinkEvents(t, runtimeevents.KindTurnCompleted, map[string]any{"usage": usage})
	require.Len(t, got, 2)
	assert.Equal(t, llmtypes.EventUsage, got[0].Type)
	require.NotNil(t, got[0].Usage)
	assert.Equal(t, 10, got[0].Usage.InputTokens)
	assert.Equal(t, 5, got[0].Usage.OutputTokens)
	assert.Equal(t, llmtypes.EventDone, got[1].Type)
	assert.Equal(t, 1, done)
}

// go-agent-wrapper sums a turn's usage, provider cost included, onto its
// terminal event; the sink hands the cost on with the tokens
// (CW-20260912-0003).
func TestWrapperSink_TurnCompletedUsageCarriesProviderCost(t *testing.T) {
	usage := &llmtypes.Usage{InputTokens: 36, OutputTokens: 1804, CacheReadTokens: 132711, CacheCreationTokens: 20565, CostUSD: 0.1904}
	got, _ := sinkEvents(t, runtimeevents.KindTurnCompleted, map[string]any{"usage": usage})
	require.NotEmpty(t, got)
	require.NotNil(t, got[0].Usage)
	assert.Equal(t, *usage, *got[0].Usage)
}

func TestWrapperSink_TurnCompletedBare_EmitsDone(t *testing.T) {
	got, done := sinkEvents(t, runtimeevents.KindTurnCompleted, nil)
	require.Len(t, got, 1)
	assert.Equal(t, llmtypes.EventDone, got[0].Type)
	assert.Equal(t, 1, done)
}

func TestWrapperSink_TurnFailedWithUsage_EmitsUsageThenError(t *testing.T) {
	usage := &llmtypes.Usage{InputTokens: 3, OutputTokens: 1}
	got, done := sinkEvents(t, runtimeevents.KindTurnFailed, map[string]any{
		"error":  "wrapper: process exited before the turn completed",
		"reason": "process_exited",
		"usage":  usage,
	})
	require.Len(t, got, 2)
	assert.Equal(t, llmtypes.EventUsage, got[0].Type)
	assert.Equal(t, llmtypes.EventError, got[1].Type)
	assert.Equal(t, "wrapper: process exited before the turn completed", got[1].Error)
	assert.Zero(t, done)
}

// Each agent.delta shape the wrapper sends maps to one stream event. ACP
// thought chunks from Claude, Codex, OpenCode and Pi carry only
// `phase: "thought"`; they are thinking, not agent output.
func TestWrapperSink_DeltaShapes(t *testing.T) {
	cases := []struct {
		name     string
		payload  map[string]any
		thinking string
		content  string
	}{
		{"native text", map[string]any{"content": "hi", "phase": "final"}, "", "hi"},
		{"ACP message", map[string]any{"content": "hi", "phase": "message", "block_id": "m1"}, "", "hi"},
		{"native thinking", map[string]any{"thinking": map[string]any{"Thinking": "hmm", "Signature": "s"}, "phase": "thought"}, "hmm", ""},
		{"copilot ACP thought", map[string]any{"content": "hmm", "thinking": true, "phase": "thought"}, "hmm", ""},
		{"ACP thought with phase only", map[string]any{"content": "hmm", "phase": "thought", "block_id": "t1"}, "hmm", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := sinkEvents(t, runtimeevents.KindAgentDelta, tc.payload)
			require.Len(t, got, 1)
			if tc.thinking != "" {
				assert.Equal(t, llmtypes.EventThinking, got[0].Type)
				require.NotNil(t, got[0].ThinkingBlock)
				assert.Equal(t, tc.thinking, got[0].ThinkingBlock.Thinking)
				assert.Empty(t, got[0].Content, "thinking must not be recorded as agent output")
				return
			}
			assert.Equal(t, llmtypes.EventDelta, got[0].Type)
			assert.Equal(t, tc.content, got[0].Content)
		})
	}
}

// A failed turn reaches a long-lived run's terminal-failure channel with
// the provider's message as its reason (CW-20261001-0169). opencode serve's
// failure is its whole session.error event, the second report with a stack
// trace; the reason is the message's first line, redacted.
func TestWrapperSink_TurnFailedSignalsTerminalFailure(t *testing.T) {
	const opencodeEvent = `{"id":"evt_1","type":"session.error","properties":{"sessionID":"ses_1","error":{"name":"UnknownError","data":{"message":"ProviderModelNotFoundError: Model not found: opencode/claude-sonnet-4-5. Did you mean: claude-sonnet-4-5?\n    at <anonymous> (/$bunfs/root/chunk.js:439:94601)"}}}}`
	cases := []struct {
		name, err, want string
	}{
		{"opencode serve session.error", opencodeEvent, "ProviderModelNotFoundError: Model not found: opencode/claude-sonnet-4-5. Did you mean: claude-sonnet-4-5?"},
		{"plain message", "wrapper: process exited before the turn completed", "wrapper: process exited before the turn completed"},
		{"JSON with a top-level message", `{"message":"rate limited"}`, "rate limited"},
		{"error as a string", `{"error":"invalid model"}`, "invalid model"},
		{"long", strings.Repeat("x", 600), strings.Repeat("x", turnFailureMaxLen) + "…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failures := make(chan string, 1)
			fanout := make(chan llmtypes.StreamEvent, 8)
			s := &torqueRuntimeEventSink{fanout: fanout, terminalFailure: failures}
			raw, err := json.Marshal(map[string]any{"error": tc.err})
			require.NoError(t, err)
			require.NoError(t, s.Write(context.Background(), runtimeevents.Event{Kind: runtimeevents.KindTurnFailed, Payload: raw}))
			select {
			case got := <-failures:
				assert.Equal(t, tc.want, got)
			default:
				t.Fatal("a failed turn must signal the run's terminal failure")
			}
			ev := <-fanout
			assert.Equal(t, llmtypes.EventError, ev.Type, "the stream still gets the error")
			assert.Equal(t, tc.want, ev.Error, "the stream carries the same message as the run's reason")
		})
	}

	// A sink with no run waiting (manual sessions, ModeOneShot) is unchanged.
	s := &torqueRuntimeEventSink{fanout: make(chan llmtypes.StreamEvent, 2)}
	require.NoError(t, s.Write(context.Background(), runtimeevents.Event{Kind: runtimeevents.KindTurnFailed, Payload: json.RawMessage(`{"error":"x"}`)}))

	// The reason is redacted like the rest of the session's output.
	failures := make(chan string, 1)
	r := redact.New("sk-test-secret-value")
	s = &torqueRuntimeEventSink{fanout: make(chan llmtypes.StreamEvent, 2), terminalFailure: failures, redact: r}
	require.NoError(t, s.Write(context.Background(), runtimeevents.Event{Kind: runtimeevents.KindTurnFailed, Payload: json.RawMessage(`{"error":"auth failed for key sk-test-secret-value"}`)}))
	assert.NotContains(t, <-failures, "sk-test-secret-value")
}

// The reason is redacted before it is cut: the redactor matches whole
// secrets, so a secret straddling the bound would otherwise keep its
// prefix. The cut lands on a rune boundary.
func TestTurnFailureText_RedactsBeforeCutting(t *testing.T) {
	const secret = "sk-straddle-secret-0123456789"
	r := redact.New(secret)
	straddling := strings.Repeat("x", turnFailureMaxLen-10) + secret
	got := turnFailureText(straddling, r)
	assert.NotContains(t, got, "sk-strad", "no prefix of the secret survives the cut")
	assert.True(t, strings.HasPrefix(got, strings.Repeat("x", turnFailureMaxLen-10)+redact.Marker[:10]))

	// A secret inside opencode's event is redacted in the message read from it.
	ev := `{"type":"session.error","properties":{"sessionID":"ses_1","error":{"name":"UnknownError","data":{"message":"401 for key ` + secret + `"}}}}`
	assert.Equal(t, "401 for key "+redact.Marker, turnFailureText(ev, r))

	multibyte := strings.Repeat("x", turnFailureMaxLen-1) + "é and more"
	got = turnFailureText(multibyte, nil)
	assert.True(t, utf8.ValidString(got), "the cut never splits a rune")
	assert.Equal(t, strings.Repeat("x", turnFailureMaxLen-1)+"…", got)
}

// An opencode error with no message gives its name.
func TestTurnFailureText_NameWithoutMessage(t *testing.T) {
	ev := `{"type":"session.error","properties":{"sessionID":"ses_1","error":{"name":"ProviderAuthError","data":{}}}}`
	assert.Equal(t, "ProviderAuthError", turnFailureText(ev, nil))
}

// opencode serve reports some errors that do not end its turn; agentkit
// makes each one a failed turn all the same. Those stay in the stream and
// do not end a long-lived run. Any other failure does, including opencode's
// model-not-found (TestWrapperSink_TurnFailedSignalsTerminalFailure).
func TestWrapperSink_OpencodeErrorsThatContinue(t *testing.T) {
	const ours = "ses_ours"
	event := func(sessionID, name, message string) string {
		props := map[string]any{"error": map[string]any{"name": name, "data": map[string]any{"message": message}}}
		if sessionID != "" {
			props["sessionID"] = sessionID
		}
		b, err := json.Marshal(map[string]any{"id": "evt_1", "type": "session.error", "properties": props})
		require.NoError(t, err)
		return string(b)
	}
	globalWrapped := `{"directory":"/w","payload":{"type":"session.error","properties":{"error":{"name":"UnknownError","data":{"message":"Failed to parse skill review"}}}}}`
	cases := []struct {
		name, err, providerSession, want string
		ends                             bool
	}{
		{"context overflow: opencode compacts and continues", event(ours, "ContextOverflowError", "prompt is too long"), ours, "prompt is too long", false},
		{"aborted: only Torque's own Stop aborts", event(ours, "MessageAbortedError", "aborted"), ours, "aborted", false},
		{"no session: a skill opencode cannot parse", event("", "UnknownError", "Failed to parse skill review: bad frontmatter"), ours, "Failed to parse skill review: bad frontmatter", false},
		{"no session, wrapped as /global/event sends it", globalWrapped, "", "Failed to parse skill review", false},
		{"another session's error", event("ses_child", "UnknownError", "child failed"), ours, "child failed", false},
		{"a session, before the wrapper knows ours", event("ses_child", "UnknownError", "model not found"), "", "model not found", true},
		{"ours: model not found", event(ours, "UnknownError", "Model not found: x"), ours, "Model not found: x", true},
		{"ours: a provider error", event(ours, "APIError", "overloaded"), ours, "overloaded", true},
		{"another runtime's failure", "process exited", ours, "process exited", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failures := make(chan string, 1)
			fanout := make(chan llmtypes.StreamEvent, 8)
			s := &torqueRuntimeEventSink{fanout: fanout, terminalFailure: failures}
			raw, err := json.Marshal(map[string]any{"error": tc.err})
			require.NoError(t, err)
			require.NoError(t, s.Write(context.Background(), runtimeevents.Event{
				Kind: runtimeevents.KindTurnFailed, Payload: raw,
				Process: runtimeevents.Process{ProviderSessionID: tc.providerSession},
			}))
			ev := <-fanout
			assert.Equal(t, llmtypes.EventError, ev.Type, "the stream gets the error either way")
			assert.Equal(t, tc.want, ev.Error)
			select {
			case got := <-failures:
				assert.True(t, tc.ends, "this error must not end the run (got %q)", got)
				assert.Equal(t, tc.want, got)
			default:
				assert.False(t, tc.ends, "this failure must end the run")
			}
		})
	}
}
