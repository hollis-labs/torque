package agent

import (
	"encoding/json"
	"strings"
	"sync"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// bootCaptureLines bounds how many stdout and stderr lines a session keeps
// from before it is ready; bootCaptureLineMax bounds each line.
const (
	bootCaptureLines   = 64
	bootCaptureLineMax = 1024
	// bootFailureDetailMax bounds the detail Boot appends to its error,
	// which becomes the run's ErrorMessage.
	bootFailureDetailMax = 2048
)

// bootCapture keeps what a go-agent-wrapper session prints before it is
// ready (CW-20261001-0105). A subprocess-per-turn session runs its first
// turn inside runtime.Start (AutoFireFirstTurn). When that turn fails,
// wrapper.Run returns before its event translator starts, so the turn's
// parsed events never reach the sink, and Boot only had "process exited 1".
// The wrapper's stdout and stderr stream writers do emit during Start:
// stdout carries agentkit's rendering of each parsed event (content,
// `[error] <msg>`, `[tool_use:<name>]`, `[turn_done]`; see agentkit
// encodeStreamEvent), stderr the CLI's own lines. bootCapture holds a
// bounded tail of both until session.ready, when it is dropped: a ready
// session's events arrive through the translator as usual.
type bootCapture struct {
	mu     sync.Mutex
	ready  bool
	stdout []string
	stderr []string
}

func (c *bootCapture) markReady() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ready = true
	c.stdout, c.stderr = nil, nil
}

func (c *bootCapture) addStdout(line string) { c.add(&c.stdout, line) }
func (c *bootCapture) addStderr(line string) { c.add(&c.stderr, line) }

func (c *bootCapture) add(dst *[]string, line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready {
		return
	}
	if len(line) > bootCaptureLineMax {
		line = line[:bootCaptureLineMax] + "…"
	}
	*dst = append(*dst, line)
	if n := len(*dst); n > bootCaptureLines {
		*dst = append([]string(nil), (*dst)[n-bootCaptureLines:]...)
	}
}

func (c *bootCapture) lines() (stdout, stderr []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.stdout...), append([]string(nil), c.stderr...)
}

// streamLinePayload is go-agent-wrapper's stdout.line / stderr.line payload.
type streamLinePayload struct {
	Line string `json:"line"`
}

func decodeStreamLine(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var p streamLinePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", false
	}
	return p.Line, true
}

// bootErrorPrefixes mark the stdout lines that report why a turn failed:
// agentkit renders an error event as `[error] <msg>`, and a login failure
// as `[auth_failed] <msg>` (agentkit v0.14.0).
var bootErrorPrefixes = []string{"[error] ", "[auth_failed] "}

func bootErrorLine(line string) (string, bool) {
	for _, p := range bootErrorPrefixes {
		if msg, ok := strings.CutPrefix(line, p); ok {
			return strings.TrimSpace(msg), true
		}
	}
	return "", false
}

// streamEventFromCaptured turns a captured stdout line back into the event
// agentkit rendered it from, for stream.jsonl.
func streamEventFromCaptured(line string) (llmtypes.StreamEvent, bool) {
	switch {
	case strings.TrimSpace(line) == "":
		return llmtypes.StreamEvent{}, false
	case line == "[turn_done]":
		return llmtypes.StreamEvent{Type: llmtypes.EventDone}, true
	case strings.HasPrefix(line, "[tool_use:") && strings.HasSuffix(line, "]"):
		name := strings.TrimSuffix(strings.TrimPrefix(line, "[tool_use:"), "]")
		return llmtypes.StreamEvent{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{Name: name}}, true
	}
	if msg, ok := bootErrorLine(line); ok {
		return llmtypes.StreamEvent{Type: llmtypes.EventError, Error: msg}, true
	}
	return llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: line}, true
}

// flushBootFailure is Boot's failure path for a session that never became
// ready. It writes the captured first-turn output where a healthy session's
// output goes, the rebuilt events to stream.jsonl and the output lines to
// session.log (stderr lines are there already: handleStderrLine writes them
// as they arrive), and returns a bounded detail for the boot error: the
// provider's error lines (else the output tail) and the stderr tail. failErr
// is left out of the detail when an error line only repeats it (agentkit
// synthesizes `[error] runner: process exited 1` for a turn that reported
// no error of its own). Call it before the sidecars close.
func (s *torqueRuntimeEventSink) flushBootFailure(failErr error) string {
	if s == nil || s.capture == nil {
		return ""
	}
	stdout, stderr := s.capture.lines()
	if len(stdout) == 0 && len(stderr) == 0 {
		return ""
	}
	if len(stdout) > 0 && s.stderr != nil {
		_, _ = s.stderr.Write([]byte("[torque] the first turn failed before the session was ready; its output:\n" + strings.Join(stdout, "\n") + "\n"))
	}
	failText := ""
	if failErr != nil {
		failText = failErr.Error()
	}
	var errs []string
	seen := map[string]bool{}
	for _, line := range stdout {
		if ev, ok := streamEventFromCaptured(line); ok {
			s.sidecar.Write(ev)
		}
		if msg, ok := bootErrorLine(line); ok && msg != "" && !seen[msg] && !strings.Contains(failText, msg) {
			seen[msg] = true
			errs = append(errs, msg)
		}
	}
	var parts []string
	switch {
	case len(errs) > 0:
		parts = append(parts, "provider error: "+strings.Join(errs, " | "))
	case len(stdout) > 0:
		parts = append(parts, "output: "+tailText(stdout, bootFailureDetailMax/2))
	}
	if len(stderr) > 0 {
		parts = append(parts, "stderr: "+tailText(stderr, bootFailureDetailMax/2))
	}
	if len(parts) == 0 {
		return ""
	}
	detail := "; " + strings.Join(parts, "; ")
	if len(detail) > bootFailureDetailMax {
		detail = detail[:bootFailureDetailMax-len("…")] + "…"
	}
	return detail
}

// tailText joins lines with " ⏎ " and keeps the last max bytes.
func tailText(lines []string, max int) string {
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, strings.TrimSpace(l))
		}
	}
	text := strings.Join(nonEmpty, " ⏎ ")
	if len(text) > max {
		text = "…" + text[len(text)-max:]
	}
	return text
}
