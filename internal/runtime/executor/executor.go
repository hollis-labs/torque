package executor

import "context"

// Executor is the interface that all executor plugins must implement.
// Torque core schedules and manages lifecycle; executors do the actual work.
type Executor interface {
	// Name returns the executor's registered name (e.g., "cli", "api").
	Name() string

	// Run executes a job and returns the result. The EventCallback receives
	// streaming events (logs, signals, artifacts, progress, token usage)
	// during execution. cb may be nil if the caller doesn't need events.
	Run(ctx context.Context, job *ExecutionJob, cb EventCallback) (*ExecutionResult, error)

	// Capabilities reports what this executor supports.
	Capabilities() ExecutorCapabilities

	// Validate checks whether a job is valid for this executor before dispatch.
	Validate(job *ExecutionJob) error
}

// EventCallback receives streaming events from an executing job.
type EventCallback func(event ExecutionEvent)
