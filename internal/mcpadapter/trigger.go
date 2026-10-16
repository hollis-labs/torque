package mcpadapter

import (
	"context"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stampTrigger records the trigger for every tools/call so each state event it
// causes carries it (CW-20261016-0133). The kind comes from the SURFACE, not
// from who the caller claims or proves to be (architect ruling,
// torque_trigger_kind_from_surface_2026_10_16): every MCP call, remote and
// loopback worker alike, is an agent call, in every auth mode. Identity goes
// in the event's actor block and nowhere else, so there is never an
// "unknown" kind and an unverified caller is never labelled human.
func stampTrigger() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
				ctx = sqlstore.ContextWithTrigger(ctx, sqlstore.Trigger{Kind: sqlstore.TriggerAgent, Ref: "mcp:" + call.Params.Name})
			}
			return next(ctx, method, req)
		}
	}
}
