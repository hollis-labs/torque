package mcpadapter

import (
	"context"
	"testing"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// capture runs the middleware over a tools/call and returns the trigger that
// a store write made with the handler's context would be stamped with.
func capture(t *testing.T, ctx context.Context) sqlstore.Trigger {
	t.Helper()
	return captureWith(t, ctx, sqlstore.TriggerHuman)
}

func captureWith(t *testing.T, ctx context.Context, unverified string) sqlstore.Trigger {
	t.Helper()
	var got sqlstore.Trigger
	h := stampTrigger(unverified)(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		// A state change with no explicit trigger takes the context's.
		got = sqlstore.ResolveTrigger(ctx)
		return nil, nil
	})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "torque_task_transition"}}
	_, err := h(ctx, "tools/call", req)
	require.NoError(t, err)
	return got
}

func TestStampTrigger_AgentAndHuman(t *testing.T) {
	agent := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "msg://agent/x/1", Kind: auth.KindAgent})
	require.Equal(t, sqlstore.Trigger{Kind: sqlstore.TriggerAgent, Ref: "mcp:torque_task_transition"}, capture(t, agent))

	// Unverified or non-agent callers are not claimed to be agents.
	unverified := auth.WithPrincipal(context.Background(), auth.Principal{ID: "x", Kind: auth.KindAgent})
	require.Equal(t, sqlstore.TriggerHuman, capture(t, unverified).Kind)
	require.Equal(t, sqlstore.TriggerHuman, capture(t, context.Background()).Kind)
}

// A loopback adapter serves one agent worker, so its unverified calls are
// agent calls; the remote surface keeps human. Wrong is worse than missing.
func TestStampTrigger_LoopbackAdapterIsAgent(t *testing.T) {
	require.Equal(t, sqlstore.TriggerAgent, (&Adapter{loopbackTaskID: "CW-X-1"}).unverifiedTrigger())
	require.Equal(t, sqlstore.TriggerHuman, (&Adapter{}).unverifiedTrigger())
	require.Equal(t, sqlstore.Trigger{Kind: sqlstore.TriggerAgent, Ref: "mcp:torque_task_transition"},
		captureWith(t, context.Background(), (&Adapter{loopbackTaskID: "CW-X-1"}).unverifiedTrigger()))
}
