package scheduler

import "context"

type launchProgressDetailKey struct{}

// WithLaunchProgressDetail sets an explicit override for dispatches using ctx.
// An empty value inherits; a task's inherited detail is never promoted to a
// launch override. dispatchTask validates before writing a run.
// This seam currently has no production caller. A future caller must scope it
// to one task dispatch, never a whole Tick (which can dispatch multiple tasks).
func WithLaunchProgressDetail(ctx context.Context, detail string) context.Context {
	return context.WithValue(ctx, launchProgressDetailKey{}, detail)
}
