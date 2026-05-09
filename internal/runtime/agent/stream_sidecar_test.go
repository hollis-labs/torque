package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenStreamSidecar_WritesJSONL is the basic happy path: open a sidecar,
// write a few events, close, read back, assert each line is valid JSON with
// the expected fields.
func TestOpenStreamSidecar_WritesJSONL(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	s := openStreamSidecar(logDir)
	require.NotNil(t, s)
	require.NotNil(t, s.f, "expected file to be open")

	s.Write(provider.StreamEvent{Type: provider.EventDelta, Content: "hello"})
	s.Write(provider.StreamEvent{Type: provider.EventToolUse, ToolUse: &provider.ToolUseBlock{
		ID:    "tu-1",
		Name:  "search",
		Input: map[string]any{"q": "go"},
	}})
	s.Write(provider.StreamEvent{Type: provider.EventUsage, Usage: &provider.Usage{
		InputTokens:  100,
		OutputTokens: 200,
	}})
	s.Write(provider.StreamEvent{Type: provider.EventError, Error: "boom"})
	s.Write(provider.StreamEvent{Type: provider.EventDone})
	s.Close()

	path := filepath.Join(logDir, "stream.jsonl")
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var lines []streamEventLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var line streamEventLine
		require.NoError(t, json.Unmarshal(sc.Bytes(), &line))
		lines = append(lines, line)
	}
	require.NoError(t, sc.Err())
	require.Len(t, lines, 5)

	assert.Equal(t, "delta", lines[0].Type)
	assert.Equal(t, "hello", lines[0].Content)

	assert.Equal(t, "tool_use", lines[1].Type)
	require.NotNil(t, lines[1].ToolUse)
	assert.Equal(t, "search", lines[1].ToolUse.Name)
	assert.Equal(t, "go", lines[1].ToolUse.Input["q"])

	assert.Equal(t, "usage", lines[2].Type)
	require.NotNil(t, lines[2].Usage)
	assert.Equal(t, 100, lines[2].Usage.InputTokens)
	assert.Equal(t, 200, lines[2].Usage.OutputTokens)

	assert.Equal(t, "error", lines[3].Type)
	assert.Equal(t, "boom", lines[3].Error)

	assert.Equal(t, "done", lines[4].Type)

	for i, line := range lines {
		assert.False(t, line.Ts.IsZero(), "line %d: ts should be populated", i)
	}
}

// TestOpenStreamSidecar_DegradesOnEmptyDir verifies that an empty workspace
// log dir produces a sidecar that no-ops on Write — the producer-never-blocks
// contract.
func TestOpenStreamSidecar_DegradesOnEmptyDir(t *testing.T) {
	s := openStreamSidecar("")
	require.NotNil(t, s)
	require.Nil(t, s.f, "expected no file when dir is empty")

	// Write should not panic, not create any file.
	s.Write(provider.StreamEvent{Type: provider.EventDelta, Content: "ignored"})
	s.Close()
}

// TestOpenStreamSidecar_DegradesOnUnopenableDir verifies that an unwritable
// dir produces a no-op sidecar (and logs a warning, not asserted here).
func TestOpenStreamSidecar_DegradesOnUnopenableDir(t *testing.T) {
	// Use a path under a regular file — MkdirAll will fail because the parent
	// is a file, not a dir.
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("not a dir"), 0o644))
	logDir := filepath.Join(blocker, "logs")

	s := openStreamSidecar(logDir)
	require.NotNil(t, s)
	require.Nil(t, s.f, "expected no file when MkdirAll fails")

	s.Write(provider.StreamEvent{Type: provider.EventDelta, Content: "ignored"})
	s.Close()
}

// TestStreamSidecar_CloseIdempotent verifies sync.Once on Close.
func TestStreamSidecar_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := openStreamSidecar(filepath.Join(dir, "logs"))
	require.NotNil(t, s.f)

	s.Close()
	s.Close() // second Close should not panic
	s.Close()
}

// TestStartStreamFanout_DrainAndForward verifies the goroutine drain
// (a) writes each event to the sidecar file AND
// (b) forwards each event to the downstream chan when one is provided.
func TestStartStreamFanout_DrainAndForward(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	downstream := make(chan provider.StreamEvent, 8)
	in, closer := startStreamFanout(logDir, 8, downstream)

	in <- provider.StreamEvent{Type: provider.EventDelta, Content: "first"}
	in <- provider.StreamEvent{Type: provider.EventDelta, Content: "second"}
	in <- provider.StreamEvent{Type: provider.EventDone}

	closer()
	close(downstream)

	// File should contain 3 lines.
	data, err := os.ReadFile(filepath.Join(logDir, "stream.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Len(t, lines, 3)

	// Downstream should have received 3 events.
	var received []provider.StreamEvent
	for ev := range downstream {
		received = append(received, ev)
	}
	require.Len(t, received, 3)
	assert.Equal(t, "first", received[0].Content)
	assert.Equal(t, "second", received[1].Content)
	assert.Equal(t, provider.EventDone, received[2].Type)
}

// TestStartStreamFanout_NilDownstream verifies the drain works when the
// caller passes nil downstream (the non-OneShot path).
func TestStartStreamFanout_NilDownstream(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	in, closer := startStreamFanout(logDir, 4, nil)
	in <- provider.StreamEvent{Type: provider.EventDelta, Content: "only-sidecar"}
	closer()

	data, err := os.ReadFile(filepath.Join(logDir, "stream.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "only-sidecar")
}

// TestStartStreamFanout_DownstreamClosedNoPanic verifies forward-on-closed
// is recovered gracefully — the sidecar still records the event.
func TestStartStreamFanout_DownstreamClosedNoPanic(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	downstream := make(chan provider.StreamEvent, 4)
	in, closer := startStreamFanout(logDir, 4, downstream)

	// Close downstream BEFORE feeding events. The drain goroutine's
	// forwardEventNonBlocking should recover the send-on-closed-chan panic.
	close(downstream)

	in <- provider.StreamEvent{Type: provider.EventDelta, Content: "downstream-closed"}
	// closer() closes `in` and waits for the drain goroutine to flush+exit;
	// no need for an arbitrary sleep here.
	closer()

	data, err := os.ReadFile(filepath.Join(logDir, "stream.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "downstream-closed",
		"sidecar should record the event even when downstream is closed")
}

// TestStartStreamFanout_DownstreamFullDrops verifies forward drops when the
// downstream chan is full (non-blocking). Sidecar still records.
func TestStartStreamFanout_DownstreamFullDrops(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	// 1-deep downstream; we fill it to capacity, then send extras.
	downstream := make(chan provider.StreamEvent, 1)
	downstream <- provider.StreamEvent{Type: provider.EventDelta, Content: "filler"}

	in, closer := startStreamFanout(logDir, 4, downstream)

	const extra = 5
	for i := 0; i < extra; i++ {
		in <- provider.StreamEvent{Type: provider.EventDelta, Content: "extra"}
	}
	closer()

	// Sidecar should have all 5 extras.
	data, err := os.ReadFile(filepath.Join(logDir, "stream.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	assert.Len(t, lines, extra)

	// Downstream should still only have the filler — extras dropped on full.
	close(downstream)
	count := 0
	for range downstream {
		count++
	}
	assert.Equal(t, 1, count, "downstream should keep only the filler; extras dropped on full")
}

// TestStartStreamFanout_CloserIdempotent verifies sync.Once on the returned
// closer.
func TestStartStreamFanout_CloserIdempotent(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	in, closer := startStreamFanout(logDir, 4, nil)
	in <- provider.StreamEvent{Type: provider.EventDone}

	closer()
	closer() // second close should not panic
	closer()
}

// TestStartStreamFanout_ConcurrentWriters verifies the drain handles many
// concurrent producers without dropping events to the sidecar.
func TestStartStreamFanout_ConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	in, closer := startStreamFanout(logDir, 64, nil)

	const writers = 8
	const perWriter = 25
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				in <- provider.StreamEvent{Type: provider.EventDelta, Content: "w"}
			}
		}(i)
	}
	wg.Wait()
	closer()

	data, err := os.ReadFile(filepath.Join(logDir, "stream.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	assert.Len(t, lines, writers*perWriter, "expected every event to land in the sidecar")
}
