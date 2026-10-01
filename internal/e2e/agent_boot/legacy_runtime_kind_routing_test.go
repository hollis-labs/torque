package agent_boot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// TestSendTurn_RoutesStoredLegacyRuntimeKinds is CW-20261001-0063: a session
// row written before the runtimes.Mode spellings keeps its stored token,
// reads back through the session scan as the current mode, and SendTurn
// routes a turn by that mode. app-server goes through JSON-RPC Call,
// serve-http and cli go through raw SendInput.
func TestSendTurn_RoutesStoredLegacyRuntimeKinds(t *testing.T) {
	cases := []struct {
		stored  string
		mode    string
		jsonRPC bool
	}{
		{stored: "app-server", mode: "jsonrpc-stdio", jsonRPC: true},
		{stored: "serve-http", mode: "http-sse"},
		{stored: "cli", mode: "subprocess-per-turn"},
	}
	for _, tc := range cases {
		t.Run(tc.stored, func(t *testing.T) {
			cd := composeDeps(t, fakeRuntimeConfig{
				JsonRpcResponses: map[string]json.RawMessage{
					"thread/start": json.RawMessage(`{"thread":{"id":"thread-legacy"}}`),
				},
			}, "claude-code")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			sess, err := cd.Manager.Boot(ctx, agent.Options{
				TaskID: "CW-LEGACY-ROW", AgentProfile: "torque-backend", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
			fake := cd.Runtime.lastSession()
			require.NotNil(t, fake)

			// Make the live session's row look like one written before the
			// rename, the way an older Torque stored it.
			_, err = cd.DB.Exec(`UPDATE sessions SET runtime_kind = ? WHERE id = ?`, tc.stored, sess.ID)
			require.NoError(t, err)

			got, err := cd.Manager.Get(sess.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.mode, got.RuntimeKind, "the stored %q must read back as its current mode", tc.stored)

			inputsBefore := fake.recordedSendInputCount()
			callsBefore := len(fake.recordedJsonRpcCalls())
			require.NoError(t, cd.Manager.SendTurn(ctx, got, "steer after the rename"))

			calls := fake.recordedJsonRpcCalls()[callsBefore:]
			inputs := fake.recordedSendInputs()[inputsBefore:]
			if tc.jsonRPC {
				require.NotEmpty(t, calls, "a %s row must be steered over JSON-RPC", tc.stored)
				assert.Equal(t, "turn/start", calls[len(calls)-1].Method)
				assert.Empty(t, inputs, "a %s row must not be steered with raw SendInput", tc.stored)
				return
			}
			assert.Empty(t, calls, "a %s row must not be steered over JSON-RPC", tc.stored)
			require.Len(t, inputs, 1)
			assert.Equal(t, "steer after the rename", string(inputs[0]), "a %s row gets the plain turn text", tc.stored)
		})
	}
}
