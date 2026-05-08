package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
)

// writeWaitResponse is a thin presentation layer over agent.Manager.Wait's
// (code, err) tuple. The handler-side wiring is tested separately end-to-end
// in internal/e2e/agent_boot/feature_test.go::TestBoot_ExitErrorCausePropagation;
// these unit tests pin the HTTP-shape contract so future edits don't silently
// change the response body for any of the four termination paths.
//
// Per go-agent-sessions v0.7.0: termination errors (*ExitError, *exec.ExitError)
// are the SUCCESS path of a wait — the session ended; here's how. They surface
// as 200 OK with structured fields. Reserves 4xx/5xx for wait-operation
// failures (ErrSessionNotRunning → 404, context cancellation → 408).

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

func TestWriteWaitResponse_CleanExit(t *testing.T) {
	w := httptest.NewRecorder()
	writeWaitResponse(w, "SES-CLEAN", 0, nil)
	require.Equal(t, http.StatusOK, w.Code)
	got := decodeBody(t, w)
	assert.Equal(t, "SES-CLEAN", got["id"])
	assert.EqualValues(t, 0, got["exit_code"])
	assert.NotContains(t, got, "cause")
	assert.NotContains(t, got, "error")
}

func TestWriteWaitResponse_NonZeroCleanExit(t *testing.T) {
	// Non-zero exit with nil err — preserves code in body, no termination
	// metadata. (Lib v0.7.0: this shape isn't typical — non-zero usually
	// produces *ExitError or *exec.ExitError — but the helper handles it.)
	w := httptest.NewRecorder()
	writeWaitResponse(w, "SES-NZ", 7, nil)
	require.Equal(t, http.StatusOK, w.Code)
	got := decodeBody(t, w)
	assert.EqualValues(t, 7, got["exit_code"])
}

func TestWriteWaitResponse_SupervisedTermination_ExitError(t *testing.T) {
	xe := &agentsessions.ExitError{
		Code:   137,
		Signal: 9,
		Killed: true,
		Cause:  agentsessions.CauseWatchdogKill,
	}
	w := httptest.NewRecorder()
	writeWaitResponse(w, "SES-WD", 137, xe)

	// Termination is the success path of a wait — 200 OK with structured body.
	require.Equal(t, http.StatusOK, w.Code)
	got := decodeBody(t, w)
	assert.Equal(t, "SES-WD", got["id"])
	assert.EqualValues(t, 137, got["exit_code"])
	assert.Equal(t, agentsessions.CauseWatchdogKill, got["cause"])
	assert.EqualValues(t, 9, got["signal"])
	assert.Equal(t, true, got["killed"])
	assert.NotContains(t, got, "error",
		"structured ExitError must NOT also surface a plain 'error' string")
}

func TestWriteWaitResponse_SupervisedTermination_Wrapped(t *testing.T) {
	// Wrapped *ExitError still extracts via errors.As — verifies the helper
	// uses errors.As (not type assertion) on the error chain.
	xe := &agentsessions.ExitError{
		Code:  124,
		Cause: agentsessions.CauseIdleTimeout,
	}
	wrapped := &wrapErr{inner: xe, msg: "supervisor: " + xe.Error()}

	w := httptest.NewRecorder()
	writeWaitResponse(w, "SES-IDLE", 124, wrapped)
	require.Equal(t, http.StatusOK, w.Code)
	got := decodeBody(t, w)
	assert.Equal(t, agentsessions.CauseIdleTimeout, got["cause"])
	assert.EqualValues(t, 124, got["exit_code"])
}

func TestWriteWaitResponse_GenericTerminationError(t *testing.T) {
	// Non-ExitError termination — surfaces under "error" string instead of
	// structured cause/signal/killed.
	someErr := errors.New("subprocess died unexpectedly")
	w := httptest.NewRecorder()
	writeWaitResponse(w, "SES-GENERIC", 1, someErr)
	require.Equal(t, http.StatusOK, w.Code)
	got := decodeBody(t, w)
	assert.EqualValues(t, 1, got["exit_code"])
	assert.Equal(t, "subprocess died unexpectedly", got["error"])
	assert.NotContains(t, got, "cause")
}

func TestWriteWaitResponse_NotRunning_404(t *testing.T) {
	w := httptest.NewRecorder()
	writeWaitResponse(w, "SES-MISSING", 0, agent.ErrSessionNotRunning)
	require.Equal(t, http.StatusNotFound, w.Code)
	// Error envelope shape from writeError; just verify the status + that
	// the body isn't pretending the session terminated cleanly.
	got := decodeBody(t, w)
	assert.NotContains(t, got, "exit_code")
	assert.NotContains(t, got, "cause")
}

func TestWriteWaitResponse_ContextCanceled_408(t *testing.T) {
	w := httptest.NewRecorder()
	writeWaitResponse(w, "SES-CANCEL", 0, context.Canceled)
	require.Equal(t, http.StatusRequestTimeout, w.Code)
	got := decodeBody(t, w)
	assert.NotContains(t, got, "exit_code",
		"wait-operation failure must NOT surface as a termination outcome")
}

func TestWriteWaitResponse_DeadlineExceeded_408(t *testing.T) {
	w := httptest.NewRecorder()
	writeWaitResponse(w, "SES-DEADLINE", 0, context.DeadlineExceeded)
	require.Equal(t, http.StatusRequestTimeout, w.Code)
}

// wrapErr is a small wrap-and-unwrap shim used to verify writeWaitResponse
// uses errors.As to walk the error chain (not bare type assertion).
type wrapErr struct {
	inner error
	msg   string
}

func (w *wrapErr) Error() string { return w.msg }
func (w *wrapErr) Unwrap() error { return w.inner }
