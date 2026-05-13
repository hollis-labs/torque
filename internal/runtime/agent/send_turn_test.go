package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendTurn_NilSessionRejected pins the input-validation contract:
// SendTurn refuses a nil *Session and a Session with an empty ID. Both
// would otherwise NPE inside the JsonRpcCall / SendInput path; the
// explicit check at the SendTurn boundary keeps the failure-mode close
// to the call site for forensic clarity.
func TestSendTurn_NilSessionRejected(t *testing.T) {
	mgr := &Manager{}

	t.Run("nil session", func(t *testing.T) {
		err := mgr.SendTurn(t.Context(), nil, "hello")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil session")
	})

	t.Run("empty session ID", func(t *testing.T) {
		err := mgr.SendTurn(t.Context(), &Session{}, "hello")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty ID")
	})
}

// TestSendTurn_CodexThreadCacheRoundTrip exercises the per-Manager
// codex thread cache helpers directly. Tests the in-memory map shape
// (sessID → threadID) without spinning up a JsonRpcCaller — the
// SendTurn-level handshake test lives in the e2e package
// (TestBoot_ModeOneShot_CodexJsonRpcStdio_Handshake) where the
// fakeSession.Call surface is wired.
func TestSendTurn_CodexThreadCacheRoundTrip(t *testing.T) {
	mgr := &Manager{}

	// Empty cache returns "", false.
	id, ok := mgr.lookupCodexThread("ses-abc")
	assert.False(t, ok)
	assert.Empty(t, id)

	// After cache, lookup returns the stored id + true.
	mgr.cacheCodexThread("ses-abc", "thread-123")
	id, ok = mgr.lookupCodexThread("ses-abc")
	assert.True(t, ok)
	assert.Equal(t, "thread-123", id)

	// Different session has its own slot.
	mgr.cacheCodexThread("ses-def", "thread-456")
	id, ok = mgr.lookupCodexThread("ses-def")
	assert.True(t, ok)
	assert.Equal(t, "thread-456", id)

	// Forget drops the slot; lookup returns "", false again.
	mgr.forgetCodexThread("ses-abc")
	_, ok = mgr.lookupCodexThread("ses-abc")
	assert.False(t, ok, "forgetCodexThread should drop the entry")

	// Other entries unaffected.
	id, ok = mgr.lookupCodexThread("ses-def")
	assert.True(t, ok)
	assert.Equal(t, "thread-456", id)

	// Forget on an absent key is a no-op (safe to call from
	// teardownSession regardless of whether the session was JsonRpcStdio).
	mgr.forgetCodexThread("ses-never-seen")
}
