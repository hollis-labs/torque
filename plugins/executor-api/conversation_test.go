package executorapi_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	executorapi "github.com/hollis-labs/clockwork-manifold/plugins/executor-api"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

func TestRunConversationDone(t *testing.T) {
	mock := &MockProvider{
		response: "CLOCKWORK_DONE",
		tokens:   executor.TokenUsage{PromptTokens: 10, CompletionTokens: 5, Cost: 0.001},
	}

	req := executorapi.CompletionRequest{
		Model:    "test-model",
		Messages: []executorapi.Message{{Role: "user", Content: "hello"}},
	}

	var events []executor.ExecutionEvent
	cb := func(e executor.ExecutionEvent) { events = append(events, e) }

	result, err := executorapi.RunConversation(context.Background(), mock, req, cb)
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
	assert.Equal(t, 10, result.Tokens.PromptTokens)
	assert.Equal(t, 5, result.Tokens.CompletionTokens)

	// Should have a signal event and a token event.
	var gotSignal, gotTokens bool
	for _, ev := range events {
		if ev.Type == executor.EventSignal && ev.Signal == "CLOCKWORK_DONE" {
			gotSignal = true
		}
		if ev.Type == executor.EventTokenUsage {
			gotTokens = true
		}
	}
	assert.True(t, gotSignal, "expected CLOCKWORK_DONE signal event")
	assert.True(t, gotTokens, "expected token usage event")
}

func TestRunConversationBlocked(t *testing.T) {
	mock := &MockProvider{
		response: "CLOCKWORK_BLOCKED: need more info",
		tokens:   executor.TokenUsage{PromptTokens: 8, CompletionTokens: 3},
	}

	req := executorapi.CompletionRequest{
		Model:    "test-model",
		Messages: []executorapi.Message{{Role: "user", Content: "do something"}},
	}

	result, err := executorapi.RunConversation(context.Background(), mock, req, nil)
	require.NoError(t, err)
	assert.Equal(t, "blocked", result.Status)
	assert.Equal(t, "need more info", result.Reason)
}
