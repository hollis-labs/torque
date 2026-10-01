package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/hollis-labs/agentkit/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"

	"github.com/hollis-labs/torque/internal/redact"
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

	// redact scrubs the launch's secrets from every event before it is
	// persisted or fanned out, and from captured lines before they are
	// truncated (CW-20261001-0123). Stderr lines reach stderr already
	// redacted (redactStderr). Nil redacts nothing.
	redact *redact.Redactor

	// capture keeps stdout/stderr from before session.ready, so Boot can
	// report a first turn that fails inside runtime.Start
	// (flushBootFailure, CW-20261001-0105). Nil disables it.
	capture *bootCapture

	// opencodePerms answers an opencode serve session's permission prompts
	// from the stdout lines carrying its SSE frames (CW-20261001-0148).
	// Nil for every other session.
	opencodePerms *opencodePermissionResponder

	// terminalFailure, when non-nil, receives the reason of every failed
	// turn: a long-lived scheduler run ends on it at once
	// (outcomeTerminalFailure), as it does for a Codex app-server turn that
	// completes as failed. Without it a turn the provider refused, such as
	// opencode serve's session.error for an unknown model, left the task in
	// doing until the inactivity threshold (CW-20261001-0169).
	terminalFailure chan<- string

	// sessionLost and resumeProgress carry a resume attempt's outcome to
	// Manager.bootWithFreshFallback (Options.sessionLost). Nil outside a
	// resume attempt.
	sessionLost    chan<- struct{}
	resumeProgress chan<- struct{}

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
		if s.capture != nil {
			s.capture.markReady()
		}
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
	case runtimeevents.KindSessionLost:
		// The provider no longer has the session this launch asked to resume
		// (go-agent-wrapper emits it after agentkit's SessionLost, which
		// arrives once the first turn fails: Boot has returned by then).
		signal(s.sessionLost)
	case runtimeevents.KindTurnFailed:
		s.handleTurnFailed(ctx, ev.Payload, ev.Process.ProviderSessionID)
	case runtimeevents.KindStderrLine:
		s.handleStderrLine(ev.Payload)
		if line, ok := decodeStreamLine(ev.Payload); ok && s.capture != nil {
			s.capture.addStderr(s.redact.Text(line))
		}
	case runtimeevents.KindStdoutLine:
		line, ok := decodeStreamLine(ev.Payload)
		if !ok {
			break
		}
		if s.opencodePerms != nil {
			s.opencodePerms.observe(line)
		}
		// Only kept before session.ready: afterwards every parsed event
		// reaches the sink through the wrapper's translator.
		if s.capture != nil {
			s.capture.addStdout(s.redact.Text(line))
		}
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
	switch ev.Type {
	case llmtypes.EventDelta, llmtypes.EventToolUse, llmtypes.EventDone:
		// The turn produced content or completed: a resume attempt holds.
		signal(s.resumeProgress)
	}
	ev = redactEvent(s.redact, ev)
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
// {"thinking": ev.ThinkingBlock} for EventThinking. Since go-agent-wrapper
// v0.17.0 both also carry `phase`, and `phase: "thought"` is the one marker
// every runtime sets on thinking: the ACP translators for Claude, Codex,
// OpenCode and Pi send thought chunks as {"content", "phase": "thought"}
// with no `thinking` field.
type deltaPayload struct {
	Content  string          `json:"content"`
	Thinking json.RawMessage `json:"thinking"`
	Phase    string          `json:"phase"`
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
	thinkingField := len(p.Thinking) > 0 && string(p.Thinking) != "false" && string(p.Thinking) != "null"
	if thinkingField || p.Phase == llmtypes.PhaseThinking {
		var thinking thinkingPayload
		if thinkingField && p.Thinking[0] == '{' {
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

// turnUsagePayload reads the usage go-agent-wrapper attaches to a turn's
// terminal event. Since v0.13.1 usage is no longer its own turn.completed:
// the wrapper sums it over the turn and puts it under "usage" on the turn's
// one terminal event, turn.completed or turn.failed (CW-20260930-0137).
type turnUsagePayload struct {
	Usage *llmtypes.Usage `json:"usage"`
}

func turnUsageFrom(raw json.RawMessage) *llmtypes.Usage {
	if len(raw) == 0 {
		return nil
	}
	var p turnUsagePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	return p.Usage
}

// handleTurnCompleted reconstructs the turn's usage (if it reported any) and
// then its EventDone. The order matches the legacy stream, where usage
// arrived before done, and EventDone must always follow: it is the turn
// boundary onDone (ModeOneShot) and the reminder pump wait on.
func (s *torqueRuntimeEventSink) handleTurnCompleted(_ context.Context, raw json.RawMessage) {
	if u := turnUsageFrom(raw); u != nil {
		s.emit(llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: u})
	}
	s.emit(llmtypes.StreamEvent{Type: llmtypes.EventDone})
}

// turnFailedPayload mirrors translateStreamEvent's {"error": ev.Error} shape
// for llmtypes.EventError; a failed turn can also carry the turn's usage.
type turnFailedPayload struct {
	Error string `json:"error"`
}

func (s *torqueRuntimeEventSink) handleTurnFailed(_ context.Context, raw json.RawMessage, providerSessionID string) {
	var p turnFailedPayload
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	if u := turnUsageFrom(raw); u != nil {
		s.emit(llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: u})
	}
	// The stream and the run's reason carry the same readable message, as
	// the Codex path's do; opencode serve's raw event stays in its
	// serve-http.log.
	msg := turnFailureText(p.Error, s.redact)
	s.emit(llmtypes.StreamEvent{Type: llmtypes.EventError, Error: msg})
	// Every genuine failed turn ends a long-lived run, blocked, as a Codex
	// app-server run's does, the reminder pump's turns included. An
	// opencode serve error that does not end its turn stays in the stream.
	if s.terminalFailure == nil || opencodeErrorContinues(p.Error, providerSessionID) {
		return
	}
	select {
	case s.terminalFailure <- msg:
	default:
	}
}

// turnFailureMaxLen bounds a failed turn's reason.
const turnFailureMaxLen = 500

// turnFailureText is the reason a failed turn gives its run: the provider's
// message (turnFailureMessage), redacted with r, then cut to its first line
// and at most turnFailureMaxLen bytes. Redaction comes first: the redactor
// matches whole secrets, so a cut through one would keep its prefix.
func turnFailureText(raw string, r *redact.Redactor) string {
	text := r.Text(turnFailureMessage(raw))
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimSpace(text)
	if cut := truncateBytes(text, turnFailureMaxLen); cut != text {
		text = cut + "…"
	}
	return text
}

// turnFailureEvent is a failed turn's error when it is JSON. opencode
// serve's is its whole session.error SSE event, which agentkit forwards as
// the error, with the message at properties.error.data.message and, on the
// second report, a stack trace after it; /global/event wraps the same
// event in payload. Other runtimes send the message itself, or an object
// with a message or an error.
type turnFailureEvent struct {
	Type       string             `json:"type"`
	Message    string             `json:"message"`
	Error      json.RawMessage    `json:"error"`
	Properties opencodeErrorProps `json:"properties"`
	Payload    *struct {
		Type       string             `json:"type"`
		Properties opencodeErrorProps `json:"properties"`
	} `json:"payload"`
}

type opencodeErrorProps struct {
	SessionID string          `json:"sessionID"`
	Error     json.RawMessage `json:"error"`
}

func parseTurnFailure(raw string) (turnFailureEvent, bool) {
	var ev turnFailureEvent
	text := strings.TrimSpace(raw)
	if !strings.HasPrefix(text, "{") || json.Unmarshal([]byte(text), &ev) != nil {
		return ev, false
	}
	if ev.Payload != nil && ev.Payload.Type != "" {
		ev.Type, ev.Properties = ev.Payload.Type, ev.Payload.Properties
	}
	return ev, true
}

// turnFailureMessage is the provider's message in a failed turn's error:
// the error object's message (errorMessage), the object's own message, or
// the error as sent.
func turnFailureMessage(raw string) string {
	ev, ok := parseTurnFailure(raw)
	if !ok {
		return strings.TrimSpace(raw)
	}
	for _, candidate := range []json.RawMessage{ev.Properties.Error, ev.Error} {
		if msg := errorMessage(candidate); msg != "" {
			return msg
		}
	}
	if ev.Message != "" {
		return ev.Message
	}
	return strings.TrimSpace(raw)
}

// errorMessage reads an error object's message, at data.message or message,
// or its name when it has no message (an opencode NamedError), or the error
// itself when it is a string.
func errorMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return asString
	}
	var obj struct {
		Name    string `json:"name"`
		Message string `json:"message"`
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	switch {
	case obj.Data.Message != "":
		return obj.Data.Message
	case obj.Message != "":
		return obj.Message
	}
	return obj.Name
}

// opencodeContinuingErrors are the opencode serve session.error names that
// do not end the turn. opencode (1.18.33) publishes ContextOverflowError
// from SessionProcessor.halt, then compacts the session and continues the
// turn (auto-compaction is on by default). MessageAbortedError comes only
// from an abort, which is Torque's own Stop: that ends the run itself.
var opencodeContinuingErrors = map[string]bool{
	"ContextOverflowError": true,
	"MessageAbortedError":  true,
}

// opencodeErrorContinues reports whether a failed turn is an opencode serve
// session.error that does not end the run. agentkit (serve_http_session.go)
// makes every session.error the turn's failure, including:
//   - one that names no session: opencode reports a plugin that fails to
//     load, or a skill it cannot parse ("Failed to parse skill …"), this
//     way, and carries on;
//   - one that names another session than this one's, when the wrapper
//     has its id (agentkit already drops those it can tell apart);
//   - a known error that does not end the turn (opencodeContinuingErrors).
//
// Any other failed turn, opencode's or another runtime's, ends the run.
func opencodeErrorContinues(raw, providerSessionID string) bool {
	ev, ok := parseTurnFailure(raw)
	if !ok || ev.Type != "session.error" {
		return false
	}
	sid := ev.Properties.SessionID
	if sid == "" || (providerSessionID != "" && sid != providerSessionID) {
		return true
	}
	var named struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(ev.Properties.Error, &named)
	return opencodeContinuingErrors[named.Name]
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

// signal sends on c without blocking; a nil or full channel drops it.
func signal(c chan<- struct{}) {
	if c == nil {
		return
	}
	select {
	case c <- struct{}{}:
	default:
	}
}
