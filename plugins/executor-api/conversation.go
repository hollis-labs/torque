package executorapi

import (
	"context"
	"strings"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// Provider is the interface for LLM API backends.
type Provider interface {
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}

// CompletionRequest is the input to a provider completion call.
type CompletionRequest struct {
	Model        string
	Messages     []Message        `json:"messages"`
	SystemPrompt string           `json:"system_prompt,omitempty"`
	MaxTokens    int              `json:"max_tokens,omitempty"`
	Temperature  float64          `json:"temperature,omitempty"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
}

// Message is a single conversation turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolDefinition is a stub for Plan 4 tool support.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema string `json:"input_schema"`
}

// ToolCall represents a tool invocation from the model.
type ToolCall struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// CompletionResponse is the result of a provider completion call.
type CompletionResponse struct {
	Content    string           `json:"content"`
	ToolCalls  []ToolCall       `json:"tool_calls,omitempty"`
	StopReason string           `json:"stop_reason,omitempty"`
	Tokens     executor.TokenUsage
}

// runConversation executes a single-turn conversation against the provider,
// parses CLOCKWORK_* signals from the response, emits events via cb,
// and returns the final ExecutionResult.
func runConversation(ctx context.Context, provider Provider, req CompletionRequest, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	result := &executor.ExecutionResult{
		Status: "done",
	}

	resp, err := provider.Complete(ctx, req)
	if err != nil {
		result.Status = "failed"
		result.Reason = err.Error()
		return result, nil
	}

	// Parse response content line by line for CLOCKWORK_* signals.
	for _, line := range strings.Split(resp.Content, "\n") {
		parsed := executor.ParseLine(line)
		switch parsed.Type {
		case executor.SignalDone:
			result.Status = "done"
			if cb != nil {
				cb(executor.SignalEvent("CLOCKWORK_DONE", parsed.Payload))
			}
		case executor.SignalBlocked:
			result.Status = "blocked"
			result.Reason = parsed.Payload
			if cb != nil {
				cb(executor.SignalEvent("CLOCKWORK_BLOCKED", parsed.Payload))
			}
		case executor.SignalReview:
			result.Status = "review"
			if cb != nil {
				cb(executor.SignalEvent("CLOCKWORK_REVIEW", parsed.Payload))
			}
		case executor.SignalLogLine:
			if cb != nil && strings.TrimSpace(line) != "" {
				cb(executor.LogEvent(line))
			}
		default:
			if cb != nil {
				cb(executor.SignalEvent(parsed.Type.String(), parsed.Payload))
			}
		}
	}

	// Emit token usage event.
	if cb != nil {
		cb(executor.TokenEvent(resp.Tokens.PromptTokens, resp.Tokens.CompletionTokens, resp.Tokens.Cost))
	}
	result.Tokens = resp.Tokens

	return result, nil
}

// RunConversation is the exported handle for runConversation (used in tests).
var RunConversation = runConversation
