package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
)

// A streaming-stdio resume is judged after the session is ready
// (CW-20261001-0202).
//
// A subprocess runtime runs its first turn inside runtime.Start, so a lost
// provider session fails Start with agentsessions.SessionLostError and Boot
// returns it (ResumeSession and planstart then boot fresh once, #177). A
// streaming-stdio session is ready as soon as its process is up, and the
// kickoff goes to the CLI after that. Resuming an id the CLI no longer has
// fails that first turn instead: Claude prints `No conversation found with
// session ID: <id>` on stderr, answers the frame with an
// `error_during_execution` result and exits 1. Boot had already returned, so
// the resumed worker was reported Resumed=true and ended `failed`.
//
// Nothing in the libraries reports it for this runtime. agentkit's
// agentsessions/from_adapter.go classifies a failed turn's stderr with the
// adapter's provider.SessionLostClassifier and emits session.lost (v0.20.2);
// agentsessions/streaming_stdio_session.go never does (its stderr goes
// straight to StartOptions.Stderr, :256-262, and its stdout lines are only
// parsed, :376-391), through v0.20.4. Until it does, resumeWatch makes the
// same classification here, from the stderr lines and stdout frames the
// wrapper hands the event sink, with the adapter's own IsSessionLost.

// resumeVerdictWait bounds how long Boot waits, after a resumed
// streaming-stdio session is ready, for the CLI to show whether the resume
// held. The wait ends at the earliest of: the CLI's stderr says the session
// is lost; a structured stdout frame that is not an error result (Claude's
// `system`/`init` frame comes before any model output, so a healthy resume
// ends it as soon as the CLI has loaded the session); the process exiting; or
// this bound. A timeout, like any other end without a "lost" verdict, leaves
// Boot to proceed exactly as it did before: the resume is treated as held.
// The bound only has to cover the CLI starting up and answering the kickoff
// frame with its error, which takes milliseconds once it is running.
const resumeVerdictWait = 3 * time.Second

// resumeVerdictBound is resumeVerdictWait; tests shorten it.
var resumeVerdictBound = resumeVerdictWait

// stderrTailMax bounds the stderr resumeWatch keeps for the classifier.
const resumeStderrTailMax = 4 << 10

// resumeWatch decides, from the wrapper's events, whether a resumed
// streaming-stdio session's resume held. The event sink feeds it; Boot waits
// on it. The first verdict stands.
type resumeWatch struct {
	classifier provider.SessionLostClassifier
	requested  string // the provider session id the launch resumes

	mu       sync.Mutex
	tail     []byte
	lost     bool
	lostLine string
	settled  bool
	done     chan struct{} // closed on the first verdict
}

// newResumeWatch returns the watch for a boot that resumes requested through
// adapter, or nil when it should not judge one: a boot that does not
// resume, or an adapter that cannot recognize a lost session.
func newResumeWatch(adapter provider.CLIAdapter, requested string) *resumeWatch {
	c, ok := adapter.(provider.SessionLostClassifier)
	if !ok || requested == "" {
		return nil
	}
	return &resumeWatch{classifier: c, requested: requested, done: make(chan struct{})}
}

// settle records the first verdict.
func (w *resumeWatch) settle(lost bool, line string) {
	if w.settled {
		return
	}
	w.settled, w.lost, w.lostLine = true, lost, line
	close(w.done)
}

// stderrLine feeds one stderr line. The adapter's classifier reads the tail
// of what the CLI has written to stderr; an exact match is the only way to a
// "lost" verdict.
func (w *resumeWatch) stderrLine(line string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.settled {
		return
	}
	w.tail = append(w.tail, line...)
	w.tail = append(w.tail, '\n')
	if n := len(w.tail); n > resumeStderrTailMax {
		w.tail = bytes.Clone(w.tail[n-resumeStderrTailMax:])
	}
	if w.classifier.IsSessionLost(w.tail) {
		w.settle(true, strings.TrimSpace(line))
	}
}

// stdoutLine feeds one stdout line. A structured frame that is not an error
// result shows the CLI is running the session, so the resume held. An error
// result settles nothing: a lost session is reported by exactly that frame,
// and stderr says why.
func (w *resumeWatch) stdoutLine(line string) {
	if w == nil {
		return
	}
	var frame struct {
		Type    string `json:"type"`
		IsError bool   `json:"is_error"`
	}
	if json.Unmarshal([]byte(line), &frame) != nil || frame.Type == "" {
		return
	}
	if frame.Type == "result" && frame.IsError {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.settle(false, "")
}

// sessionLost records a session.lost event, if a library version emits one
// for this runtime.
func (w *resumeWatch) sessionLost() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.settle(true, "")
}

// wait blocks until the resume's verdict, the session's process exiting
// (exited), the caller's ctx ending, or resumeVerdictBound, and returns the
// lost-session error when the verdict is "lost" and nil otherwise. A ctx
// that ends here is not a failure: Boot returns what it would have without
// the watch.
func (w *resumeWatch) wait(ctx context.Context, exited <-chan struct{}) error {
	t := time.NewTimer(resumeVerdictBound)
	defer t.Stop()
	select {
	case <-w.done:
	case <-exited:
	case <-t.C:
	case <-ctx.Done():
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.lost {
		return nil
	}
	cause := errors.New("the CLI has no conversation with that session id")
	if w.lostLine != "" {
		cause = errors.New(w.lostLine)
	}
	return &agentsessions.SessionLostError{RequestedID: w.requested, Err: cause}
}
