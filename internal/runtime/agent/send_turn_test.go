package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/agentkit/agentruntime/turn"
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

// TestSendTurn_CodexThreadCacheRoundTrip exercises the per-Manager codex
// app-server cache field. The full SendTurn-level handshake test lives in the
// e2e package where the fakeSession.Call surface is wired.
func TestSendTurn_CodexThreadCacheRoundTrip(t *testing.T) {
	mgr := &Manager{}
	rpc := testCodexRPC{threadID: "thread-123"}

	// Empty cache returns "", false.
	id, ok := mgr.codexTurns.ThreadID("ses-abc")
	assert.False(t, ok)
	assert.Empty(t, id)

	// After cache, lookup returns the stored id + true.
	err := mgr.codexTurns.SendTurn(t.Context(), "ses-abc", rpc, "hello", turn.CodexAppServerOptions{
		ClientName: "torque",
		CWD:        "/tmp/work",
	})
	require.NoError(t, err)
	id, ok = mgr.codexTurns.ThreadID("ses-abc")
	assert.True(t, ok)
	assert.Equal(t, "thread-123", id)

	// Different session has its own slot.
	rpc.threadID = "thread-456"
	err = mgr.codexTurns.SendTurn(t.Context(), "ses-def", rpc, "hello", turn.CodexAppServerOptions{})
	require.NoError(t, err)
	id, ok = mgr.codexTurns.ThreadID("ses-def")
	assert.True(t, ok)
	assert.Equal(t, "thread-456", id)

	// Forget drops the slot; lookup returns "", false again.
	mgr.codexTurns.Forget("ses-abc")
	_, ok = mgr.codexTurns.ThreadID("ses-abc")
	assert.False(t, ok, "codexTurns.Forget should drop the entry")

	// Other entries unaffected.
	id, ok = mgr.codexTurns.ThreadID("ses-def")
	assert.True(t, ok)
	assert.Equal(t, "thread-456", id)

	// Forget on an absent key is a no-op (safe to call from
	// teardownSession regardless of whether the session was JsonRpcStdio).
	mgr.codexTurns.Forget("ses-never-seen")
}

type testCodexRPC struct {
	threadID string
}

func (r testCodexRPC) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method == "thread/start" {
		return json.RawMessage(`{"thread":{"id":"` + r.threadID + `"}}`), nil
	}
	return json.RawMessage(`{}`), nil
}
