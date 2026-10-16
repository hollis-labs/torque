package mcpadapter

import (
	"context"
	"testing"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// capture runs the middleware over a tools/call and returns the trigger that a
// store write made with the handler's context would be stamped with.
func capture(t *testing.T, ctx context.Context) sqlstore.Trigger {
	t.Helper()
	var got sqlstore.Trigger
	h := stampTrigger()(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		got = sqlstore.ResolveTrigger(ctx)
		return nil, nil
	})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "torque_task_transition"}}
	_, err := h(ctx, "tools/call", req)
	require.NoError(t, err)
	return got
}

// The kind comes from the surface: every MCP call is an agent call, whatever
// the principal is or is not (architect ruling, trigger kind from surface).
func TestStampTrigger_EveryMCPCallIsAgent(t *testing.T) {
	want := sqlstore.Trigger{Kind: sqlstore.TriggerAgent, Ref: "mcp:torque_task_transition"}

	verifiedAgent := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "msg://agent/x/1", Kind: auth.KindAgent})
	verifiedOperator := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "msg://user/x/1", Kind: auth.KindOperator})
	unverified := auth.WithPrincipal(context.Background(), auth.Principal{ID: "operator(off-mode)", Kind: auth.KindOperator})

	for name, ctx := range map[string]context.Context{
		"verified agent":    verifiedAgent,
		"verified operator": verifiedOperator,
		"off-mode operator": unverified,
		"no principal":      context.Background(),
	} {
		t.Run(name, func(t *testing.T) { require.Equal(t, want, capture(t, ctx)) })
	}
}
