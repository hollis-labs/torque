package mcpadapter

import (
	"context"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// authorizeTools checks every tools/call against the guard's tool-scope
// table (internal/mcpscopes) using the principal the HTTP middleware stamped
// into ctx. Off mode lets the call through; permissive logs "would deny";
// enforce refuses with auth.ErrUnauthenticated or auth.ErrForbidden.
func authorizeTools(g *auth.Guard) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
				if err := g.AuthorizeTool(ctx, call.Params.Name); err != nil {
					return nil, err
				}
			}
			return next(ctx, method, req)
		}
	}
}
