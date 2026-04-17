package executor

import (
	"fmt"
	"time"
)

// ExecutionJob is the self-contained execution contract passed to an executor.
// Built from a TaskRecord's fields by the scheduler.
type ExecutionJob struct {
	TaskID       string
	RunID        int64
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
	// Metadata mirrors the task's freeform metadata JSON (map form). Keys
	// that executors recognize include timeout_seconds_override (int seconds,
	// valid range 60..7200). Nil when the task has no metadata set.
	Metadata map[string]any
}

// Validate checks that the job has all required fields.
func (j *ExecutionJob) Validate() error {
	if j.TaskID == "" {
		return fmt.Errorf("job missing TaskID")
	}
	return nil
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

// EffectiveTimeout returns the configured max duration, or a default of 5 minutes.
func (l ExecutionLimits) EffectiveTimeout() time.Duration {
	if l.MaxDuration != nil {
		return *l.MaxDuration
	}
	return 5 * time.Minute
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
	Cost             float64
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

// SignalEvent creates an ExecutionEvent for a CLOCKWORK_* signal.
func SignalEvent(signal, payload string) ExecutionEvent {
	return ExecutionEvent{Type: EventSignal, Signal: signal, Content: payload}
}

// LogEvent creates an ExecutionEvent for a log line.
func LogEvent(content string) ExecutionEvent {
	return ExecutionEvent{Type: EventLog, Content: content}
}

// TokenEvent creates an ExecutionEvent for token usage.
func TokenEvent(prompt, completion int, cost float64) ExecutionEvent {
	return ExecutionEvent{
		Type:   EventTokenUsage,
		Tokens: &TokenUsage{PromptTokens: prompt, CompletionTokens: completion, Cost: cost},
	}
}

// ArtifactEvent creates an ExecutionEvent for an artifact produced during execution.
func ArtifactEvent(a Artifact) ExecutionEvent {
	return ExecutionEvent{Type: EventArtifact, Artifact: &a}
}
