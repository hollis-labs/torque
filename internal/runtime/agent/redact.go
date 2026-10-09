package agent

import (
	"bytes"
	"io"
	"strings"
	"sync"

	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"

	"github.com/hollis-labs/torque/internal/redact"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// launchRedactor builds the redactor for what a session persists: its
// session.log and per-run stderr log, stream.jsonl, and the run's error
// (CW-20261001-0123). An agent CLI's error output can echo a credential
// from its environment. The secrets are the values of every secret-named
// variable (executor.LooksLikeSecret: *TOKEN*, *API_KEY*, *SECRET*, ...)
// in planted (the provider's env amendments) and envs: the session's
// composed env, the daemon's own env (a secret FilterEnv withholds from the
// CLI can still reach it through planted config or a helper), and the mux
// env. Other values, such as paths, stay readable.
func launchRedactor(planted map[string]string, envs ...[]string) *redact.Redactor {
	var values []string
	for k, v := range planted {
		if executor.LooksLikeSecret(k) {
			values = append(values, v)
		}
	}
	for _, env := range envs {
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok && executor.LooksLikeSecret(k) {
				values = append(values, v)
			}
		}
	}
	return redact.New(values...)
}

// redactEvent returns ev with r applied to its text: content, error,
// thinking and tool-use input. The tool-use and thinking blocks are copied,
// never edited in place, since the event's producer may still hold them.
func redactEvent(r *redact.Redactor, ev llmtypes.StreamEvent) llmtypes.StreamEvent {
	if r == nil {
		return ev
	}
	ev.Content = r.Text(ev.Content)
	ev.Error = r.Text(ev.Error)
	if ev.ToolUse != nil {
		tu := *ev.ToolUse
		if tu.Input != nil {
			tu.Input, _ = redactValue(r, tu.Input).(map[string]any)
		}
		ev.ToolUse = &tu
	}
	if ev.ThinkingBlock != nil {
		tb := *ev.ThinkingBlock
		tb.Thinking = r.Text(tb.Thinking)
		ev.ThinkingBlock = &tb
	}
	return ev
}

// redactValue returns a copy of a decoded JSON value with r applied to
// every string in it.
func redactValue(r *redact.Redactor, v any) any {
	switch t := v.(type) {
	case string:
		return r.Text(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = redactValue(r, x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = redactValue(r, x)
		}
		return out
	}
	return v
}

// redactLineMax bounds how much of one unterminated line redactingWriter
// holds before passing it on.
const redactLineMax = 64 << 10

// redactingWriter redacts whole lines before they reach w. It holds a
// partial line until its newline (or flush), so a secret split across two
// writes is still matched. The wrapper path writes stderr a line at a time;
// the legacy path copies the pipe in arbitrary chunks.
type redactingWriter struct {
	mu  sync.Mutex
	w   io.Writer
	r   *redact.Redactor
	buf []byte
}

func (rw *redactingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.buf = append(rw.buf, p...)
	end := bytes.LastIndexByte(rw.buf, '\n') + 1
	if end == 0 {
		if len(rw.buf) < redactLineMax {
			return len(p), nil
		}
		end = len(rw.buf)
	}
	_, err := io.WriteString(rw.w, rw.r.Text(string(rw.buf[:end])))
	rw.buf = append(rw.buf[:0], rw.buf[end:]...)
	return len(p), err
}

func (rw *redactingWriter) flush() {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if len(rw.buf) > 0 {
		_, _ = io.WriteString(rw.w, rw.r.Text(string(rw.buf)))
		rw.buf = rw.buf[:0]
	}
}

// redactStderr wraps a session's stderr writer and its closer (from
// openStderrSidecar) so each line is redacted before it reaches
// session.log, the per-run stderr log or the tail, and the closer writes
// out an unterminated last line before closing them.
func redactStderr(w io.Writer, closer func(), r *redact.Redactor) (io.Writer, func()) {
	if r == nil {
		return w, closer
	}
	rw := &redactingWriter{w: w, r: r}
	return rw, func() {
		rw.flush()
		closer()
	}
}
