package executor_test

import (
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
