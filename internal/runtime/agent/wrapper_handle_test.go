package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/testutil/testenv"
)

// TestWrapperHandleLifetime pins CW-20261001-0041: Stop's teardownSession
// must leave a wrapper-routed session's handle registered, so a Wait after
// Stop blocks on the real exit. Only the run goroutine removes it, once
// wr.Run has returned (finishWrapperSession); a handle that finished before
// Boot registered it is never registered.
func TestWrapperHandleLifetime(t *testing.T) {
	m := NewManager(&Dependencies{WorkspacesRoot: testenv.WorkspacesRoot(t)})
	h := &wrapperHandle{runDone: make(chan struct{})}
	m.registerWrapperSession("SES-W", h)

	m.teardownSession("SES-W")
	got, ok := m.wrapperHandleFor("SES-W")
	require.True(t, ok, "teardown (Stop) must not drop a handle whose run has not returned")
	assert.Same(t, h, got)

	// Wait blocks until the run goroutine finishes.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, err := m.Wait(ctx, "SES-W")
	cancel()
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "Wait must block while the run is live, got %v", err)

	m.finishWrapperSession("SES-W", h)
	close(h.runDone)
	_, ok = m.wrapperHandleFor("SES-W")
	assert.False(t, ok, "the run goroutine's finish drops the handle")
	code, err := h.wait(context.Background())
	assert.NoError(t, err)
	assert.Zero(t, code)

	// A run that finished before Boot got to register it stays unregistered.
	late := &wrapperHandle{runDone: make(chan struct{})}
	m.finishWrapperSession("SES-LATE", late)
	m.registerWrapperSession("SES-LATE", late)
	_, ok = m.wrapperHandleFor("SES-LATE")
	assert.False(t, ok)
}
