package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lineEvent(kind runtimeevents.EventKind, line string) runtimeevents.Event {
	raw, _ := json.Marshal(map[string]string{"line": line})
	return runtimeevents.Event{Kind: kind, Payload: raw}
}

// CW-20261001-0105: what a session prints before it is ready is kept,
// bounded, and flushed with a bounded detail when Boot fails.
func TestBootCapture_FlushBootFailure(t *testing.T) {
	logDir := t.TempDir()
	var sessionLog bytes.Buffer
	s := &torqueRuntimeEventSink{capture: &bootCapture{}, stderr: &sessionLog, sidecar: openStreamSidecar(logDir)}
	ctx := context.Background()
	for _, ev := range []runtimeevents.Event{
		lineEvent(runtimeevents.KindStdoutLine, "partial reply"),
		lineEvent(runtimeevents.KindStdoutLine, "[error] Model not found: opencode/claude-sonnet-4-5"),
		lineEvent(runtimeevents.KindStdoutLine, "[error] runner: process exited 1"),
		lineEvent(runtimeevents.KindStderrLine, "warn: agent file missing"),
	} {
		require.NoError(t, s.Write(ctx, ev))
	}
	detail := s.flushBootFailure(errors.New("wrapper: runtime.Start: agentsessions: auto-fire first turn: runner: process exited 1"))
	s.sidecar.Close()

	assert.Contains(t, detail, "provider error: Model not found: opencode/claude-sonnet-4-5")
	assert.NotContains(t, detail, "provider error: Model not found: opencode/claude-sonnet-4-5 | runner", "an error line that repeats the boot error is left out")
	assert.Contains(t, detail, "stderr: warn: agent file missing")
	assert.Contains(t, sessionLog.String(), "[error] Model not found", "the captured output lands in session.log")
	assert.Contains(t, sessionLog.String(), "warn: agent file missing", "stderr is still teed as it arrives")

	stream, err := os.ReadFile(filepath.Join(logDir, "stream.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(stream), `"type":"error"`)
	assert.Contains(t, string(stream), "Model not found")
	assert.Contains(t, string(stream), "partial reply")
}

func TestBootCapture_ReadyDropsAndStopsCapturing(t *testing.T) {
	s := &torqueRuntimeEventSink{capture: &bootCapture{}}
	ctx := context.Background()
	require.NoError(t, s.Write(ctx, lineEvent(runtimeevents.KindStdoutLine, "[error] before ready")))
	require.NoError(t, s.Write(ctx, runtimeevents.Event{Kind: runtimeevents.KindSessionReady}))
	require.NoError(t, s.Write(ctx, lineEvent(runtimeevents.KindStdoutLine, "[error] after ready")))
	assert.Empty(t, s.flushBootFailure(nil), "a ready session's output is delivered by the translator, not the capture")
}

func TestBootCapture_Bounds(t *testing.T) {
	s := &torqueRuntimeEventSink{capture: &bootCapture{}}
	ctx := context.Background()
	for i := 0; i < 3*bootCaptureLines; i++ {
		require.NoError(t, s.Write(ctx, lineEvent(runtimeevents.KindStderrLine, fmt.Sprintf("stderr %d %s", i, strings.Repeat("x", 200)))))
		require.NoError(t, s.Write(ctx, lineEvent(runtimeevents.KindStdoutLine, fmt.Sprintf("[error] e%d %s", i, strings.Repeat("y", 3*bootCaptureLineMax)))))
	}
	stdout, stderr := s.capture.lines()
	assert.Len(t, stdout, bootCaptureLines)
	assert.Len(t, stderr, bootCaptureLines)
	assert.LessOrEqual(t, len(stdout[0]), bootCaptureLineMax+len("…"))
	assert.Contains(t, stderr[len(stderr)-1], fmt.Sprintf("stderr %d ", 3*bootCaptureLines-1), "the tail is kept")
	detail := s.flushBootFailure(nil)
	assert.LessOrEqual(t, len(detail), bootFailureDetailMax)
}
