package agent

import (
	"context"
	"encoding/json"
	"io"

	"github.com/hollis-labs/agentkit/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
)

// torqueRuntimeEventSink implements runtimeevents.Sink -- the seam
// wrapper.Config.Activity's Bridge writes every emitted event to for the
// go-agent-wrapper-routed boot path (CW-20260904-0098). It reconstructs the
// llmtypes.StreamEvent shape Torque's existing downstream (translateStreamEvent,
// the last-activity gate, the stream.jsonl sidecar) already consumes -- none of
// that logic changes, only its event source moves from
// agentsessions.StartOptions.EventFanout to this synchronous callback.
//
// The reconstruction mirrors apps/nanite/internal/runtime/agent/wrapper_sink.go,
// which reverse-engineers the same canonical wire shapes go-agent-wrapper's own
// wrapper/event_translator.go produces (translateStreamEvent/translateProviderEvent) --
// this is the wrapper's real wire contract, not a Nanite-specific convention.
//
// Also doubles as the Boot-time readiness signal: wrapper.Wrapper.Run sets its
// internal session handle immediately before emitting runtimeevents.KindSessionReady
// (unconditionally, every runtime kind), so observing that Kind here is the one
// point at which wr.SendInput/wr.Stop become safe to call -- Boot blocks on it
// (via onReady) before returning a *Session to its own caller, mirroring
// mgr.inner.Start() returning nil on the legacy path.
type torqueRuntimeEventSink struct {
	sessID  string
	mgr     *Manager
	deps    *Dependencies
	sidecar *streamSidecar
	fanout  chan<- llmtypes.StreamEvent

	// stderr, when non-nil, receives KindStderrLine content -- the
	// wrapper-path equivalent of the legacy path's StartOptions.Stderr tee
	// to <workspace>/logs/session.log (CW-20260417-0024/CW-20260508-0006).
	// wr.Run() routes stderr through runtimeevents instead of a caller
	// io.Writer (see go-agent-wrapper's io_streams.go), so the sink is the
	// only place left to preserve that forensic surface.
	stderr io.Writer

	onReady func()
	onDone  func()
}

var _ runtimeevents.Sink = (*torqueRuntimeEventSink)(nil)

// Write is invoked synchronously from wrapper.Wrapper.Run's translator
// goroutine (per runtimeevents.Sink's own doc contract) -- one goroutine per
// session, so no locking is needed around sidecar/fanout/gate access here,
// matching the single-drain-goroutine invariant stream_sidecar.go's
// startStreamFanout relied on for the legacy path.
func (s *torqueRuntimeEventSink) Write(ctx context.Context, ev runtimeevents.Event) error {
	switch ev.Kind {
	case runtimeevents.KindSessionReady:
		if s.onReady != nil {
			s.onReady()
		}
	case runtimeevents.KindSessionHeartbeat:
		// Replaces pid_poller.go's periodic touchSessionUnlessFrozen tick for
		// wrapper-routed sessions: Config.HeartbeatInterval drives this Kind
		// at a fixed cadence regardless of real activity, so it feeds ONLY
		// the liveness heartbeat, never the content-based freeze/unfreeze
		// gate below (which stays keyed off real translated stream events).
		s.mgr.touchSessionUnlessFrozen(ctx, s.sessID)
	case runtimeevents.KindProcessStarted:
		// Replaces pid_poller.go's PID-changed write for wrapper-routed
		// sessions: records the real subprocess pid for the orphan sweep's
		// syscall.Kill(pid, 0) liveness check, same as the legacy poller did.
		// wrapper.Wrapper.Run emits the pid in the payload ({"pid": N}), not
		// on Event.Process (wrapper.go:613-616) -- Process.PID is never set
		// by the wrapper itself.
		var p struct {
			PID int `json:"pid"`
		}
		if len(ev.Payload) > 0 && json.Unmarshal(ev.Payload, &p) == nil && p.PID > 0 && s.deps != nil {
			_ = s.deps.UpdateSessionState(ctx, s.sessID, string(agentsessions.StateRunning), p.PID, nil)
		}
	case runtimeevents.KindAgentDelta:
		s.handleDelta(ctx, ev.Payload)
	case runtimeevents.KindAgentToolUse:
		s.handleToolUse(ctx, ev.Payload)
	case runtimeevents.KindTurnCompleted:
		s.handleTurnCompleted(ctx, ev.Payload)
	case runtimeevents.KindTurnFailed:
		s.handleTurnFailed(ctx, ev.Payload)
	case runtimeevents.KindStderrLine:
		s.handleStderrLine(ev.Payload)
	default:
		// process.exited / plant.* / sandbox.* / interrupt.* / raw stdio --
		// no legacy llmtypes.StreamEvent equivalent. wr.Run()'s own return
		// value (observed by Boot's goroutine) is the terminal-state signal;
		// nothing here needs to react to process.exited specifically.
	}
	return nil
}

// emit reconstructs and fans out a translated llmtypes.StreamEvent exactly
// where stream_sidecar.go's startStreamFanout drain loop used to: write the
// forensic JSONL line, flip the last-activity gate, forward to the
// executor's fanout channel (translateStreamEvent's cost/plan-phase-emitter
// consumer), and fire onDone for ModeOneShot's turn-complete wait.
func (s *torqueRuntimeEventSink) emit(ev llmtypes.StreamEvent) {
	s.sidecar.Write(ev)
	s.mgr.observeStreamEvent(s.sessID, ev)
	if s.fanout != nil {
		forwardEventNonBlocking(s.fanout, ev)
	}
	if ev.Type == llmtypes.EventDone && s.onDone != nil {
		s.onDone()
	}
}

// deltaPayload mirrors translateStreamEvent's two llmtypes.StreamEvent ->
// KindAgentDelta shapes: {"content": ev.Content} for EventDelta and
// {"thinking": ev.ThinkingBlock} for EventThinking.
type deltaPayload struct {
	Content  string          `json:"content"`
	Thinking json.RawMessage `json:"thinking"`
}

type thinkingPayload struct {
	Thinking  string `json:"Thinking"`
	Signature string `json:"Signature"`
}

func (s *torqueRuntimeEventSink) handleDelta(_ context.Context, raw json.RawMessage) {
	var p deltaPayload
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	if len(p.Thinking) > 0 && string(p.Thinking) != "false" && string(p.Thinking) != "null" {
		var thinking thinkingPayload
		if p.Thinking[0] == '{' {
			_ = json.Unmarshal(p.Thinking, &thinking)
		}
		if thinking.Thinking == "" {
			thinking.Thinking = p.Content
		}
		s.emit(llmtypes.StreamEvent{
			Type: llmtypes.EventThinking,
			ThinkingBlock: &llmtypes.ThinkingBlock{
				Thinking:  thinking.Thinking,
				Signature: thinking.Signature,
			},
		})
		return
	}
	s.emit(llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: p.Content})
}

// toolUsePayload mirrors translateStreamEvent's {"tool_use": ev.ToolUse}
// shape, where ev.ToolUse is *llmtypes.ToolUseBlock{ID,Name,Input}.
type toolUsePayload struct {
	ToolUse *llmtypes.ToolUseBlock `json:"tool_use"`
}

func (s *torqueRuntimeEventSink) handleToolUse(_ context.Context, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var p toolUsePayload
	if err := json.Unmarshal(raw, &p); err != nil || p.ToolUse == nil {
		return
	}
	s.emit(llmtypes.StreamEvent{Type: llmtypes.EventToolUse, ToolUse: p.ToolUse})
}

// turnCompletedPayload mirrors translateStreamEvent's two
// llmtypes.StreamEvent -> KindTurnCompleted shapes: {"usage": ev.Usage} for
// EventUsage and a nil/empty payload for EventDone.
type turnCompletedPayload struct {
	Usage *llmtypes.Usage `json:"usage"`
}

func (s *torqueRuntimeEventSink) handleTurnCompleted(_ context.Context, raw json.RawMessage) {
	if len(raw) == 0 {
		s.emit(llmtypes.StreamEvent{Type: llmtypes.EventDone})
		return
	}
	var p turnCompletedPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.Usage == nil {
		s.emit(llmtypes.StreamEvent{Type: llmtypes.EventDone})
		return
	}
	s.emit(llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: p.Usage})
}

// turnFailedPayload mirrors translateStreamEvent's {"error": ev.Error} shape
// for llmtypes.EventError.
type turnFailedPayload struct {
	Error string `json:"error"`
}

func (s *torqueRuntimeEventSink) handleTurnFailed(_ context.Context, raw json.RawMessage) {
	var p turnFailedPayload
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	s.emit(llmtypes.StreamEvent{Type: llmtypes.EventError, Error: p.Error})
}

// stderrLinePayload mirrors go-agent-wrapper's io_streams.go newStreamWriter
// KindStderrLine shape: {"line": "..."}.
type stderrLinePayload struct {
	Line string `json:"line"`
}

func (s *torqueRuntimeEventSink) handleStderrLine(raw json.RawMessage) {
	if s.stderr == nil || len(raw) == 0 {
		return
	}
	var p stderrLinePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	_, _ = s.stderr.Write([]byte(p.Line + "\n"))
}
