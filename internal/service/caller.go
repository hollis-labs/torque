package service

import "context"

// Caller is the session-admitted identity the Tether gateway stamps on proxied
// MCP calls (X-Tether-Agent-Urn, X-Tether-Session-Id). It is only set when the
// request carries the bearer that vouches for those headers; absent means unknown.
type Caller struct {
	AgentURN  string
	SessionID string
}

type callerKey struct{}

// WithCaller attaches the gateway-vouched caller to ctx.
func WithCaller(ctx context.Context, c Caller) context.Context {
	if c.AgentURN == "" && c.SessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the gateway-vouched caller, if any.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}
