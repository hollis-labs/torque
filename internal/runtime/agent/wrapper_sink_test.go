package agent

import (
	"context"
	"encoding/json"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
