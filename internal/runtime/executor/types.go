package executor

import "time"

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
