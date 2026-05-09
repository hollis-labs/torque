package agent

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hollis-labs/go-providers/provider"
)

// streamEventLine is the JSONL shape clockwork persists to
// <workspace>/logs/stream.jsonl. Re-serialized from provider.StreamEvent —
// this is a clockwork-internal projection, NOT raw claude stream-json (the
// lib parses claude's stdout into typed StreamEvent values before the
// EventFanout consumer sees them; the original stream-json bytes are lost in
// translation). Forensic value: replay the typed event sequence,
// correlate event timestamps with DB writes, surface error events that go to
// claude's stdout in stream-json mode but never make it into a DB state
// change.
//
// CW-20260509-0001. Promote to capturing raw stream-json bytes is a follow-up
// (would require a go-providers surface change so the adapter exposes a
// stdout tee independently of typed-event translation).
type streamEventLine struct {
	Ts            time.Time               `json:"ts"`
	Type          string                  `json:"type"`
	Content       string                  `json:"content,omitempty"`
	Error         string                  `json:"error,omitempty"`
	SessionID     string                  `json:"session_id,omitempty"`
	ToolUse       *provider.ToolUseBlock  `json:"tool_use,omitempty"`
	Usage         *provider.Usage         `json:"usage,omitempty"`
	ThinkingBlock *provider.ThinkingBlock `json:"thinking_block,omitempty"`
}

// streamSidecar persists provider.StreamEvent values as JSONL to a per-session
// stream.jsonl file. Best-effort: a file-open failure degrades to a no-op,
// never blocks the producer.
type streamSidecar struct {
	f    *os.File
	once sync.Once
}

// openStreamSidecar opens <workspaceLogDir>/stream.jsonl and returns a sidecar
// that callers Write events into. Pass the directory path (typically
// <workspace>/logs/), NOT a file path. Returns a sidecar with f=nil if the
// directory is empty or the file can't be opened — Write becomes a no-op in
// that case, preserving the producer-never-blocks contract.
func openStreamSidecar(workspaceLogDir string) *streamSidecar {
	if workspaceLogDir == "" {
		return &streamSidecar{}
	}
	if err := os.MkdirAll(workspaceLogDir, 0o700); err != nil {
		log.Printf("agent: stream sidecar dir unavailable (%s): %v — degrading to no-op", workspaceLogDir, err)
		return &streamSidecar{}
	}
	p := filepath.Join(workspaceLogDir, "stream.jsonl")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("agent: stream sidecar open failed (%s): %v — degrading to no-op", p, err)
		return &streamSidecar{}
	}
	return &streamSidecar{f: f}
}

// Write serializes ev to a JSONL line and appends it to the sidecar file.
// No-op when the sidecar file failed to open. Errors during marshal or write
// are silently swallowed — the executor's other observability paths
// (translateStreamEvent → callback, lib's StateSink → DB row) are the
// authoritative surfaces; stream.jsonl is forensic-only.
func (s *streamSidecar) Write(ev provider.StreamEvent) {
	if s == nil || s.f == nil {
		return
	}
	line := streamEventLine{
		Ts:            time.Now().UTC(),
		Type:          string(ev.Type),
		Content:       ev.Content,
		Error:         ev.Error,
		SessionID:     ev.SessionID,
		ToolUse:       ev.ToolUse,
		Usage:         ev.Usage,
		ThinkingBlock: ev.ThinkingBlock,
	}
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	_, _ = s.f.Write(append(b, '\n'))
}

// Close closes the underlying file. Idempotent (sync.Once-guarded) — callers
// commonly use both `defer Close()` and an explicit Close before reading the
// file back; either pattern is safe.
func (s *streamSidecar) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.f != nil {
			_ = s.f.Close()
		}
	})
}

// startStreamFanout opens a stream sidecar at <workspaceLogDir>/stream.jsonl
// and spawns a drain goroutine that consumes from the returned channel,
// writing each event to the sidecar AND (when downstream is non-nil)
// forwarding it to the downstream channel non-blockingly.
//
// The returned `in` channel is intended to be used as
// agentsessions.StartOptions.EventFanout. The lib writes provider.StreamEvent
// values into it; this drain absorbs them.
//
// `downstream` is the executor's existing per-call fanout (currently set by
// the agent.Executor.Run path for ModeOneShot to capture token usage +
// per-event callbacks). For non-OneShot modes (orchestrator/subagent/
// background/resume), downstream is nil and the drain only writes to the
// sidecar file.
//
// `closer` shuts down the drain by closing `in` and waiting for the goroutine
// to flush + close the sidecar file. Idempotent (sync.Once). Closing
// downstream is the caller's concern — the drain never closes it.
//
// Forward-to-downstream uses a non-blocking select with `default` drop, then
// a recover-guarded send to handle the case where the downstream chan was
// closed by its owner (executor.Run's `close(fanout)` after Boot returns) —
// dropped events are still recorded in the sidecar, so forensic visibility
// is preserved.
func startStreamFanout(workspaceLogDir string, depth int, downstream chan<- provider.StreamEvent) (in chan provider.StreamEvent, closer func()) {
	sidecar := openStreamSidecar(workspaceLogDir)
	in = make(chan provider.StreamEvent, depth)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer sidecar.Close()
		for ev := range in {
			sidecar.Write(ev)
			if downstream != nil {
				forwardEventNonBlocking(downstream, ev)
			}
		}
	}()

	var once sync.Once
	closer = func() {
		once.Do(func() {
			close(in)
			wg.Wait()
		})
	}
	return in, closer
}

// forwardEventNonBlocking forwards ev to downstream best-effort. Drops the
// event when downstream is full (buffer exhausted) AND when downstream has
// been closed by its owner (recover catches the panic on send-to-closed).
// In both drop cases, the sidecar file already has the event recorded —
// forensic visibility is preserved even when the downstream consumer is
// detached.
func forwardEventNonBlocking(downstream chan<- provider.StreamEvent, ev provider.StreamEvent) {
	defer func() {
		_ = recover() // send-on-closed-chan; downstream consumer is gone
	}()
	select {
	case downstream <- ev:
	default:
	}
}
