package executor_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStreamJSONContentDelta(t *testing.T) {
	input := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Running tests...\n"}}
{"type":"content_block_delta","delta":{"type":"text_delta","text":"All tests pass\n"}}`

	var events []executor.StreamEvent
	err := executor.ParseStreamJSON(strings.NewReader(input), func(ev executor.StreamEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, executor.StreamEventLogLine, events[0].Type)
	assert.Equal(t, "Running tests...", events[0].Text)
	assert.Equal(t, executor.StreamEventLogLine, events[1].Type)
	assert.Equal(t, "All tests pass", events[1].Text)
}

func TestParseStreamJSONSignalInContent(t *testing.T) {
	input := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"CLOCKWORK_DONE\n"}}`

	var events []executor.StreamEvent
	err := executor.ParseStreamJSON(strings.NewReader(input), func(ev executor.StreamEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, executor.StreamEventSignal, events[0].Type)
	assert.Equal(t, executor.SignalDone, events[0].Signal.Type)
}

func TestParseStreamJSONBlockedSignal(t *testing.T) {
	input := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"CLOCKWORK_BLOCKED: need credentials\n"}}`

	var events []executor.StreamEvent
	err := executor.ParseStreamJSON(strings.NewReader(input), func(ev executor.StreamEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, executor.StreamEventSignal, events[0].Type)
	assert.Equal(t, executor.SignalBlocked, events[0].Signal.Type)
	assert.Equal(t, "need credentials", events[0].Signal.Payload)
}

func TestParseStreamJSONResultEvent(t *testing.T) {
	input := `{"type":"result","result":"{\"status\":\"done\",\"signal\":\"CLOCKWORK_DONE\",\"summary\":\"fixed auth\"}","input_tokens":1000,"output_tokens":500,"cost_usd":0.05}`

	var events []executor.StreamEvent
	err := executor.ParseStreamJSON(strings.NewReader(input), func(ev executor.StreamEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, executor.StreamEventResult, events[0].Type)
	require.NotNil(t, events[0].Result)
	assert.Equal(t, "done", events[0].Result.Status)
	assert.Equal(t, "CLOCKWORK_DONE", events[0].Result.Signal)
	require.NotNil(t, events[0].Tokens)
	assert.Equal(t, int64(1000), events[0].Tokens.InputTokens)
	assert.Equal(t, int64(500), events[0].Tokens.OutputTokens)
	assert.InDelta(t, 0.05, events[0].Tokens.CostUSD, 0.001)
}

func TestParseStreamJSONSkipsMalformed(t *testing.T) {
	input := `not json at all
{"type":"content_block_delta","delta":{"type":"text_delta","text":"valid line\n"}}`

	var events []executor.StreamEvent
	err := executor.ParseStreamJSON(strings.NewReader(input), func(ev executor.StreamEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)
	// Malformed line skipped, valid line parsed
	assert.Len(t, events, 1)
	assert.Equal(t, "valid line", events[0].Text)
}

func TestParseStreamJSONEmptyLines(t *testing.T) {
	input := `
{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello\n"}}
`

	var events []executor.StreamEvent
	err := executor.ParseStreamJSON(strings.NewReader(input), func(ev executor.StreamEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)
	assert.Len(t, events, 1)
}

// Claude's --verbose mode interleaves non-JSON log lines with the NDJSON
// stream. The parser must skip them without aborting the scan.
// Regression for Bug CW-20260417-0023.
func TestParseStreamJSONToleratesVerboseLogLines(t *testing.T) {
	input := `Thinking...
{"type":"content_block_delta","delta":{"type":"text_delta","text":"working\n"}}
[DEBUG] internal claude diagnostic
{"type":"content_block_delta","delta":{"type":"text_delta","text":"done step\n"}}
random trailing garbage line
{"type":"result","result":"{\"status\":\"done\",\"signal\":\"CLOCKWORK_DONE\"}"}`

	var events []executor.StreamEvent
	err := executor.ParseStreamJSON(strings.NewReader(input), func(ev executor.StreamEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)

	// We expect: 2 log-line events from deltas + 1 result event.
	var logs int
	var results int
	for _, ev := range events {
		switch ev.Type {
		case executor.StreamEventLogLine:
			logs++
		case executor.StreamEventResult:
			results++
		}
	}
	assert.Equal(t, 2, logs, "malformed lines must not mask valid deltas")
	assert.Equal(t, 1, results, "result event must still parse after noisy lines")
}

// A canonical fixture that exercises the full stream-json flow: some log
// chatter, a mid-stream signal, and a final structured result.
func TestParseStreamJSONFullFixture(t *testing.T) {
	input := `{"type":"message_start","message":{"id":"msg_1"}}
{"type":"content_block_delta","delta":{"type":"text_delta","text":"Starting work...\n"}}
{"type":"content_block_delta","delta":{"type":"text_delta","text":"CLOCKWORK_NOTE: checkpoint reached\n"}}
{"type":"content_block_delta","delta":{"type":"text_delta","text":"finishing\n"}}
{"type":"result","result":"{\"status\":\"done\",\"signal\":\"CLOCKWORK_DONE\",\"summary\":\"feature shipped\",\"files_changed\":[\"a.go\",\"b.go\"]}","input_tokens":1234,"output_tokens":456,"cost_usd":0.0123}`

	var events []executor.StreamEvent
	err := executor.ParseStreamJSON(strings.NewReader(input), func(ev executor.StreamEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)

	var result *executor.AgentResult
	var gotNote bool
	for _, ev := range events {
		if ev.Type == executor.StreamEventResult {
			result = ev.Result
			require.NotNil(t, ev.Tokens)
			assert.Equal(t, int64(1234), ev.Tokens.InputTokens)
			assert.InDelta(t, 0.0123, ev.Tokens.CostUSD, 0.0001)
		}
		if ev.Type == executor.StreamEventSignal && ev.Signal.Type == executor.SignalNote {
			gotNote = true
			assert.Equal(t, "checkpoint reached", ev.Signal.Payload)
		}
	}

	require.NotNil(t, result, "expected a structured result event")
	assert.Equal(t, "done", result.Status)
	assert.Equal(t, "CLOCKWORK_DONE", result.Signal)
	assert.Equal(t, "feature shipped", result.Summary)
	assert.Equal(t, []string{"a.go", "b.go"}, result.FilesChanged)
	assert.True(t, gotNote, "expected CLOCKWORK_NOTE signal mid-stream")
}

// The scanner must handle NDJSON lines larger than the stdlib default 64 KB
// buffer AND larger than FE's historical 1 MB cap — claude's verbose
// stream-json can emit multi-MB result lines in real runs.
func TestParseStreamJSONLargeResultLine(t *testing.T) {
	// Build a ~2 MB summary string embedded in the result. This is past
	// FE's 1 MB cap but inside our 10 MB cap.
	big := strings.Repeat("A", 2*1024*1024)
	// JSON-quoted payload: escape the outer braces via construction.
	// We embed the summary as a plain ASCII string (no escaping needed).
	payload := `{"status":"done","signal":"CLOCKWORK_DONE","summary":"` + big + `"}`
	// The result field is a JSON-encoded string, so we need to escape it
	// for the outer envelope.
	input := `{"type":"result","result":` + jsonQuote(payload) + `}`

	var events []executor.StreamEvent
	err := executor.ParseStreamJSON(strings.NewReader(input), func(ev executor.StreamEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Result)
	assert.Equal(t, "done", events[0].Result.Status)
	assert.Len(t, events[0].Result.Summary, len(big))
}

// jsonQuote returns s as a JSON-encoded string literal.
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestParseStreamJSONMultilineContent(t *testing.T) {
	input := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"line one\nCLOCKWORK_NOTE: important\nline three\n"}}`

	var events []executor.StreamEvent
	err := executor.ParseStreamJSON(strings.NewReader(input), func(ev executor.StreamEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)
	assert.Len(t, events, 3)
	assert.Equal(t, executor.StreamEventLogLine, events[0].Type)
	assert.Equal(t, "line one", events[0].Text)
	assert.Equal(t, executor.StreamEventSignal, events[1].Type)
	assert.Equal(t, executor.SignalNote, events[1].Signal.Type)
	assert.Equal(t, executor.StreamEventLogLine, events[2].Type)
}
