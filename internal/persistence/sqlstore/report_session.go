package sqlstore

import "context"

type reportSessionKey struct{}

// ContextWithReportSession is set only by the daemon's session-bound loopback.
// The ID is Torque's session row key, never a Tether envelope session ID.
func ContextWithReportSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, reportSessionKey{}, sessionID)
}
