package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	lostStderr = "No conversation found with session ID: 00000000-0000-4000-8000-0000000000ff"
	errorFrame = `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"x"}`
	initFrame  = `{"type":"system","subtype":"init","session_id":"y"}`
)

func newTestWatch(t *testing.T) *resumeWatch {
	t.Helper()
	w := newResumeWatch(&provider.ClaudeAdapter{}, "00000000-0000-4000-8000-0000000000ff")
	require.NotNil(t, w)
	return w
}

// waitFor runs w.wait with a short bound and reports what it returned and
// how long it took.
func waitFor(t *testing.T, w *resumeWatch, bound time.Duration, exited <-chan struct{}) (error, time.Duration) {
	t.Helper()
	prev := resumeVerdictBound
	resumeVerdictBound = bound
	t.Cleanup(func() { resumeVerdictBound = prev })
	start := time.Now()
	err := w.wait(context.Background(), exited)
	return err, time.Since(start)
}

func TestNewResumeWatch(t *testing.T) {
	assert.NotNil(t, newResumeWatch(&provider.ClaudeAdapter{}, "id"))
	assert.Nil(t, newResumeWatch(&provider.ClaudeAdapter{}, ""), "a boot that does not resume has nothing to judge")
	// An adapter that cannot recognize a lost session (the embedded
	// interface exposes no IsSessionLost).
	assert.Nil(t, newResumeWatch(struct{ provider.CLIAdapter }{}, "id"))
}

// Claude's lost-session shape, in the order the CLI produces it: the error
// result on stdout (which settles nothing), then the stderr line.
func TestResumeWatch_LostSession(t *testing.T) {
	w := newTestWatch(t)
	w.stdoutLine(errorFrame)
	select {
	case <-w.done:
		t.Fatal("an error result alone must not settle the resume")
	default:
	}
	w.stderrLine(lostStderr)
	err, took := waitFor(t, w, 5*time.Second, nil)
	require.Error(t, err)
	assert.Less(t, took, time.Second, "a settled verdict does not wait for the bound")

	assert.ErrorIs(t, err, provider.ErrProviderSessionLost)
	var lost *agentsessions.SessionLostError
	require.ErrorAs(t, err, &lost)
	assert.Equal(t, "00000000-0000-4000-8000-0000000000ff", lost.RequestedID)
	assert.Contains(t, err.Error(), "No conversation found")
}

// The order does not matter: stderr first, error result second.
func TestResumeWatch_LostSession_StderrFirst(t *testing.T) {
	w := newTestWatch(t)
	w.stderrLine(lostStderr)
	w.stdoutLine(errorFrame)
	err, _ := waitFor(t, w, 5*time.Second, nil)
	assert.ErrorIs(t, err, provider.ErrProviderSessionLost)
}

// A structured frame that is not an error result shows the CLI loaded the
// session: the wait ends at once with no verdict of "lost", and a stderr
// line that arrives later cannot change it.
func TestResumeWatch_HeldSession(t *testing.T) {
	w := newTestWatch(t)
	w.stdoutLine("not json, ignored")
	w.stdoutLine(initFrame)
	err, took := waitFor(t, w, 5*time.Second, nil)
	assert.NoError(t, err)
	assert.Less(t, took, time.Second, "a healthy resume pays no wait beyond its first frame")

	w.stderrLine(lostStderr)
	err, _ = waitFor(t, w, 5*time.Second, nil)
	assert.NoError(t, err, "the first verdict stands")
}

// Exact matches only: stderr that is not the CLI's lost-session message,
// across lines and over the tail's bound, is never a verdict.
func TestResumeWatch_OtherStderrIsNotLost(t *testing.T) {
	w := newTestWatch(t)
	w.stdoutLine(errorFrame)
	w.stderrLine("Invalid API key · Please run /login")
	w.stderrLine("conversation found with session")
	w.stderrLine("No conversation")
	exited := make(chan struct{})
	close(exited)
	err, took := waitFor(t, w, 5*time.Second, exited)
	assert.NoError(t, err)
	assert.Less(t, took, time.Second, "the process exiting ends the wait")
}

func TestResumeWatch_StderrTailIsBounded(t *testing.T) {
	w := newTestWatch(t)
	for range 3 * resumeStderrTailMax / 40 {
		w.stderrLine("some diagnostic line of ordinary length")
	}
	assert.LessOrEqual(t, len(w.tail), resumeStderrTailMax)
	w.stderrLine(lostStderr)
	err, _ := waitFor(t, w, 5*time.Second, nil)
	assert.ErrorIs(t, err, provider.ErrProviderSessionLost, "the match survives the bound")
}

// With no verdict, no exit and a live ctx, the wait ends at the bound and
// reports nothing: Boot then proceeds as it did before the watch existed.
func TestResumeWatch_BoundFallsBackToHeld(t *testing.T) {
	w := newTestWatch(t)
	err, took := waitFor(t, w, 150*time.Millisecond, nil)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, took, 150*time.Millisecond)
	assert.Less(t, took, 2*time.Second)
}

func TestResumeWatch_EndedCtxIsNoVerdict(t *testing.T) {
	w := newTestWatch(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prev := resumeVerdictBound
	resumeVerdictBound = 5 * time.Second
	defer func() { resumeVerdictBound = prev }()
	assert.NoError(t, w.wait(ctx, nil))
}

// A library version that emits session.lost for this runtime is honored.
func TestResumeWatch_SessionLostEvent(t *testing.T) {
	w := newTestWatch(t)
	w.sessionLost()
	err, _ := waitFor(t, w, 5*time.Second, nil)
	assert.ErrorIs(t, err, provider.ErrProviderSessionLost)
	assert.False(t, errors.Is(err, context.Canceled))
}

// The nil watch (every boot that does not resume on streaming-stdio) is
// inert: the sink calls these on every line.
func TestResumeWatch_NilIsInert(t *testing.T) {
	var w *resumeWatch
	w.stderrLine(lostStderr)
	w.stdoutLine(initFrame)
	w.sessionLost()
}
