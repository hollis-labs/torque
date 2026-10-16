package scheduler

import "context"

type launchProgressDetailKey struct{}

// WithLaunchProgressDetail sets an explicit override for dispatches using ctx.
// An empty value inherits; a task's inherited detail is never promoted to a
// launch override. dispatchTask validates before writing a run.
// Normal scheduler dispatch leaves this unset by design: there is no explicit
// launch caller, so reports inherit task/template/default detail. Explicit
// session and plan-start launches carry their own override. Any caller of this
// helper must scope it to one dispatch, never a Tick with multiple tasks.
func WithLaunchProgressDetail(ctx context.Context, detail string) context.Context {
	return context.WithValue(ctx, launchProgressDetailKey{}, detail)
}
