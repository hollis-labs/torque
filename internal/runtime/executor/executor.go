package executor

import (
	"context"
	"time"
)

// Executor is the interface that all executor plugins must implement.
// Clockwork core schedules and manages lifecycle; executors do the actual work.
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

// ExecutionJob is the self-contained execution contract passed to an executor.
// Built from a TaskRecord's fields by the scheduler.
type ExecutionJob struct {
	TaskID       string
	Description  string
	SystemPrompt string
	WorkingDir   string
	AgentProfile string
	Tools        []string
	Permissions  map[string]string
	Environment  map[string]string
	Files        []string
	Deliverables []Deliverable
	Limits       ExecutionLimits
}

// Deliverable declares a required output artifact.
type Deliverable struct {
	Type        string
	Required    bool
	Description string
}

// ExecutionLimits constrains a single execution run.
type ExecutionLimits struct {
	CostBudget  *float64
	TokenBudget *int
	MaxDuration *time.Duration
	MaxRetries  int
}

// EventCallback receives streaming events from an executing job.
type EventCallback func(event ExecutionEvent)

// EventType categorizes execution events.
type EventType string

const (
	EventLog        EventType = "log"
	EventSignal     EventType = "signal"
	EventArtifact   EventType = "artifact"
	EventProgress   EventType = "progress"
	EventTokenUsage EventType = "token_usage"
)

// ExecutionEvent is a single event emitted during execution.
type ExecutionEvent struct {
	Type     EventType
	Signal   string    // CLOCKWORK_DONE, CLOCKWORK_BLOCKED, etc. (when Type == EventSignal)
	Content  string    // Log line or signal payload
	Artifact *Artifact // When Type == EventArtifact
	Tokens   *TokenUsage
	Progress *float64 // 0.0-1.0 (when Type == EventProgress)
}

// Artifact is an output produced by an execution run.
type Artifact struct {
	Type     string
	Content  string
	URL      string
	FilePath string
	Metadata map[string]interface{}
}

// TokenUsage tracks LLM token consumption.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
}

// ExecutionResult is the final outcome of an execution run.
type ExecutionResult struct {
	Status    string        // "done", "review", "blocked", "failed"
	Reason    string
	Artifacts []Artifact
	Tokens    TokenUsage
	Cost      float64
	Duration  time.Duration
}

// ExecutorCapabilities declares what features an executor supports.
type ExecutorCapabilities struct {
	SupportsStreaming   bool
	SupportsTools       bool
	SupportsSandbox     bool
	SupportsPermissions bool
}
