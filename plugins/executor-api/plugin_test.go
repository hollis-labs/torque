package executorapi_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	executorapi "github.com/hollis-labs/clockwork-manifold/plugins/executor-api"
)

// MockProvider is a test double for the Provider interface.
type MockProvider struct {
	response string
	tokens   executor.TokenUsage
	err      error
}

func (m *MockProvider) Complete(ctx context.Context, req executorapi.CompletionRequest) (*executorapi.CompletionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &executorapi.CompletionResponse{
		Content:    m.response,
		StopReason: "end_turn",
		Tokens:     m.tokens,
	}, nil
}

// testProfiles returns a ProfileMap with a standard test profile.
func testProfiles() config.ProfileMap {
	return config.ProfileMap{
		"default": {
			Executor:  "api",
			Provider:  "mock",
			Model:     "test-model",
			MaxTokens: 1024,
		},
	}
}

// testJob returns a minimal valid ExecutionJob.
func testJob() *executor.ExecutionJob {
	return &executor.ExecutionJob{
		TaskID:       "task-1",
		Description:  "do something useful",
		AgentProfile: "default",
	}
}

func TestName(t *testing.T) {
	e := executorapi.New(testProfiles(), nil)
	assert.Equal(t, "api", e.Name())
}

func TestCapabilities(t *testing.T) {
	e := executorapi.New(testProfiles(), nil)
	caps := e.Capabilities()
	assert.True(t, caps.SupportsStreaming)
	assert.True(t, caps.SupportsTools)
	assert.False(t, caps.SupportsSandbox)
	assert.True(t, caps.SupportsPermissions)
}

func TestValidate_Valid(t *testing.T) {
	e := executorapi.New(testProfiles(), nil)
	err := e.Validate(testJob())
	assert.NoError(t, err)
}

func TestValidate_MissingTaskID(t *testing.T) {
	e := executorapi.New(testProfiles(), nil)
	job := testJob()
	job.TaskID = ""
	err := e.Validate(job)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TaskID")
}

func TestValidate_NoProvider(t *testing.T) {
	profiles := config.ProfileMap{
		"default": {
			Executor: "api",
			// Provider intentionally omitted
			Model: "test-model",
		},
	}
	e := executorapi.New(profiles, nil)
	err := e.Validate(testJob())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider")
}

func TestRunWithMockProvider_Done(t *testing.T) {
	mock := &MockProvider{
		response: "CLOCKWORK_DONE",
		tokens:   executor.TokenUsage{PromptTokens: 100, CompletionTokens: 50, Cost: 0.005},
	}

	e := executorapi.New(testProfiles(), nil, executorapi.WithProvider("mock", mock))

	var events []executor.ExecutionEvent
	cb := func(ev executor.ExecutionEvent) { events = append(events, ev) }

	result, err := e.Run(context.Background(), testJob(), cb)
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
	assert.Equal(t, 100, result.Tokens.PromptTokens)
	assert.Equal(t, 50, result.Tokens.CompletionTokens)
	assert.InDelta(t, 0.005, result.Tokens.Cost, 1e-9)
}

func TestRunBlocked(t *testing.T) {
	mock := &MockProvider{
		response: "CLOCKWORK_BLOCKED: waiting on dependency",
		tokens:   executor.TokenUsage{PromptTokens: 20, CompletionTokens: 10},
	}

	e := executorapi.New(testProfiles(), nil, executorapi.WithProvider("mock", mock))

	result, err := e.Run(context.Background(), testJob(), nil)
	require.NoError(t, err)
	assert.Equal(t, "blocked", result.Status)
	assert.Equal(t, "waiting on dependency", result.Reason)
}

func TestRunReview(t *testing.T) {
	mock := &MockProvider{
		response: "CLOCKWORK_REVIEW",
		tokens:   executor.TokenUsage{},
	}

	e := executorapi.New(testProfiles(), nil, executorapi.WithProvider("mock", mock))

	result, err := e.Run(context.Background(), testJob(), nil)
	require.NoError(t, err)
	assert.Equal(t, "review", result.Status)
}

func TestRunProviderError(t *testing.T) {
	mock := &MockProvider{
		err: errors.New("connection refused"),
	}

	e := executorapi.New(testProfiles(), nil, executorapi.WithProvider("mock", mock))

	result, err := e.Run(context.Background(), testJob(), nil)
	require.NoError(t, err) // Go error is nil; failure is in result
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Reason, "connection refused")
}
