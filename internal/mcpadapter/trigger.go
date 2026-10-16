package mcpadapter

import (
	"context"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stampTrigger records who is behind a tools/call so every state event it
// causes carries the right trigger (CW-20261016-0133). A verified agent
// principal makes it an agent call. Otherwise unverified is the caller's
// default: a loopback adapter serves exactly one agent worker, so it passes
// agent; the remote /mcp surface passes human (any unverified caller there is
// attributed to a person until the architect rules on unverified remote MCP).
// A wrong value is worse than a missing one, so each surface states its own.
func stampTrigger(unverified string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
				kind := unverified
				if p, ok := auth.FromContext(ctx); ok && p.Verified && p.Kind == auth.KindAgent {
					kind = sqlstore.TriggerAgent
				}
				ctx = sqlstore.ContextWithTrigger(ctx, sqlstore.Trigger{Kind: kind, Ref: "mcp:" + call.Params.Name})
			}
			return next(ctx, method, req)
		}
	}
}

// unverifiedTrigger is the trigger kind for a call with no verified agent
// principal: agent on a loopback adapter (one agent worker per adapter), human
// on the remote surface.
func (a *Adapter) unverifiedTrigger() string {
	if a.loopbackTaskID != "" {
		return sqlstore.TriggerAgent
	}
	return sqlstore.TriggerHuman
}
