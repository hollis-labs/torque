package tool

import (
	"context"
	"encoding/json"
)

// Category constants identify the functional group a tool belongs to.
const (
	CategoryCoreIO    = "core-io"
	CategorySearch    = "search"
	CategoryMCP       = "mcp"
	CategoryAgent     = "agent"
	CategorySession   = "session"
	CategoryExecution = "execution"
)

// Source constants identify where a tool originates.
const (
	SourceBuiltin = "builtin"
	SourceMCP     = "mcp"
	SourcePlugin  = "plugin"
	SourceUser    = "user"
)

// ExecutionContext carries per-invocation metadata that tools may inspect.
type ExecutionContext struct {
	SessionID  string
	TaskID     string
	WorkingDir string
}

// ToolResult is returned by Tool.Call.
type ToolResult struct {
	Output   string            `json:"output"`
	IsError  bool              `json:"is_error,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// PermissionRule describes a single allow/deny pattern for tool access control.
type PermissionRule struct {
	Pattern  string `json:"pattern"  yaml:"pattern"`
	Behavior string `json:"behavior" yaml:"behavior"`
}

// Tool is the interface that all Clockwork tools must implement.
type Tool interface {
	// Identity
	Name() string
	Description() string
	Category() string
	Source() string
	Tags() []string

	// Schema for input validation / LLM consumption.
	InputSchema() json.RawMessage

	// Invocation
	Call(ctx context.Context, input map[string]any, execCtx ExecutionContext) (*ToolResult, error)

	// Input validation (separate from Call so callers can pre-check).
	ValidateInput(input map[string]any) error

	// Safety predicates — may inspect input for context-sensitive answers.
	IsConcurrencySafe(input map[string]any) bool
	IsReadOnly(input map[string]any) bool
	IsDestructive(input map[string]any) bool

	// Access control hints.
	DefaultPermissions() []PermissionRule
}
