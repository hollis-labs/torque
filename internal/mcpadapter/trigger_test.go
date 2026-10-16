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
	var got sqlstore.Trigger
	h := stampTrigger()(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
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
