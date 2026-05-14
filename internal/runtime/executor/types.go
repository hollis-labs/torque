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
	// AgentFile is the task's agent_file field verbatim: absolute path, or
	// path relative to WorkingDir. The executor is responsible for resolving
	// and loading it at dispatch (see internal/agentfile). Empty when unset.
	AgentFile    string
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
	Content  string    // Log line content
	Artifact *Artifact // When Type == EventArtifact
	Tokens   *TokenUsage
	Progress *float64 // 0.0-1.0 (when Type == EventProgress)
	ToolUse  *ToolUse // When Type == EventToolUse
}

// ToolUse is the payload of an EventToolUse event. ArgsSummary is a
// truncated, secret-sanitized preview of the tool invocation's input so the
// UI can show what the agent is doing without leaking credentials.
type ToolUse struct {
	Name        string
	ArgsSummary string
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
	Status    string // "done", "review", "blocked", "failed"
	Reason    string
	Artifacts []Artifact
	Tokens    TokenUsage
	Cost      float64
	Duration  time.Duration
}

// ExecutorCapabilities declares what features an executor supports.
//
// SupportsResume signals whether the executor (or, for multi-provider executors
// like cli, the underlying adapter for a given provider) can resume a prior
// session via a native CLI/API resume primitive (e.g. `claude --resume <id>`,
// `codex resume <id>`). This is the canonical capability flag used by the
// reactor harness to decide between ResumeSession + send_input vs fresh-boot
// (CW-20260512-0059, sprint α decision D4 — no per-call probes, no try-resume-
// then-fallback runtime detection; declare once per adapter and route).
//
// For multi-provider executors the executor-level Capabilities() reports
// SupportsResume=true when ANY supported provider supports resume; per-task
// dispatch uses agent.ProviderCapabilities(provider) for the per-adapter
// answer.
type ExecutorCapabilities struct {
	SupportsStreaming   bool
	SupportsTools       bool
	SupportsSandbox     bool
	SupportsPermissions bool
	SupportsResume      bool
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

// ToolUseEvent creates an ExecutionEvent describing an in-flight tool
// invocation (Edit, Read, Bash, etc.) surfaced from a streaming agent.
func ToolUseEvent(name, argsSummary string) ExecutionEvent {
	return ExecutionEvent{
		Type:    EventToolUse,
		ToolUse: &ToolUse{Name: name, ArgsSummary: argsSummary},
	}
}
