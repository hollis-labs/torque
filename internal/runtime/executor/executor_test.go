package executor_test

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionJobDefaults(t *testing.T) {
	job := &executor.ExecutionJob{
		TaskID:      "CW-20260407-0001",
		Description: "Fix the auth bug",
	}

	assert.Equal(t, "CW-20260407-0001", job.TaskID)
	assert.Equal(t, "Fix the auth bug", job.Description)
	assert.Nil(t, job.Limits.CostBudget)
	assert.Nil(t, job.Limits.TokenBudget)
	assert.Nil(t, job.Limits.MaxDuration)
	assert.Equal(t, 0, job.Limits.MaxRetries)
}

func TestExecutionResultFields(t *testing.T) {
	result := &executor.ExecutionResult{
		Status:   "done",
		Reason:   "completed successfully",
		Cost:     0.05,
		Duration: 30 * time.Second,
		Tokens: executor.TokenUsage{
			PromptTokens:     1000,
			CompletionTokens: 500,
		},
		Artifacts: []executor.Artifact{
			{Type: "diff", Content: "--- a/file\n+++ b/file"},
		},
	}

	assert.Equal(t, "done", result.Status)
	assert.Equal(t, 0.05, result.Cost)
	assert.Equal(t, 1000, result.Tokens.PromptTokens)
	assert.Len(t, result.Artifacts, 1)
}

func TestEventTypes(t *testing.T) {
	events := []executor.EventType{
		executor.EventLog,
		executor.EventSignal,
		executor.EventArtifact,
		executor.EventProgress,
		executor.EventTokenUsage,
	}
	for _, et := range events {
		assert.NotEmpty(t, string(et))
	}
}

func TestExecutorCapabilities(t *testing.T) {
	caps := executor.ExecutorCapabilities{
		SupportsStreaming:   true,
		SupportsTools:       true,
		SupportsSandbox:     false,
		SupportsPermissions: false,
	}
	assert.True(t, caps.SupportsStreaming)
	assert.True(t, caps.SupportsTools)
	assert.False(t, caps.SupportsSandbox)
}

// Verify the Executor interface can be assigned
func TestExecutorInterfaceAssignment(t *testing.T) {
	var _ executor.Executor = (*testExecutor)(nil)
}

type testExecutor struct{}

func (e *testExecutor) Name() string { return "test" }
func (e *testExecutor) Run(ctx context.Context, job *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	return &executor.ExecutionResult{Status: "done"}, nil
}
func (e *testExecutor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{}
}
func (e *testExecutor) Validate(job *executor.ExecutionJob) error { return nil }

func TestExecutorInterfaceRun(t *testing.T) {
	var exec executor.Executor = &testExecutor{}

	result, err := exec.Run(context.Background(), &executor.ExecutionJob{
		TaskID:      "CW-20260407-0001",
		Description: "test task",
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
}

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := executor.NewRegistry()

	mock := executor.NewMockExecutor()
	reg.Register(mock)

	got, err := reg.Get("mock")
	require.NoError(t, err)
	assert.Equal(t, "mock", got.Name())
}

func TestRegistryGetNotFound(t *testing.T) {
	reg := executor.NewRegistry()

	_, err := reg.Get("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRegistryList(t *testing.T) {
	reg := executor.NewRegistry()

	mock := executor.NewMockExecutor()
	reg.Register(mock)

	names := reg.List()
	assert.Equal(t, []string{"mock"}, names)
}
