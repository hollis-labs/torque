package scheduler

import "context"

type launchProgressDetailKey struct{}

// WithLaunchProgressDetail sets an explicit override for the next dispatch
// made with ctx. An empty value inherits; a task's inherited detail is never
// promoted to a launch override. dispatchTask validates before writing a run.
func WithLaunchProgressDetail(ctx context.Context, detail string) context.Context {
	return context.WithValue(ctx, launchProgressDetailKey{}, detail)
}
