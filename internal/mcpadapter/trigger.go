package mcpadapter

import (
	"context"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stampTrigger records who is behind a tools/call so every state event it
// causes carries the right trigger (CW-20261016-0133). A verified agent
// principal makes it an agent call; anything else is attributed to a human,
// the only other thing that can reach this surface.
func stampTrigger() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
				kind := sqlstore.TriggerHuman
				if p, ok := auth.FromContext(ctx); ok && p.Verified && p.Kind == auth.KindAgent {
					kind = sqlstore.TriggerAgent
				}
				ctx = sqlstore.ContextWithTrigger(ctx, sqlstore.Trigger{Kind: kind, Ref: "mcp:" + call.Params.Name})
			}
			return next(ctx, method, req)
		}
	}
}
