package executor_test

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockExecutorImplementsInterface(t *testing.T) {
	var _ executor.Executor = (*executor.MockExecutor)(nil)
}

func TestMockExecutorDefaultSuccess(t *testing.T) {
	mock := executor.NewMockExecutor()

	result, err := mock.Run(context.Background(), &executor.ExecutionJob{
		TaskID:      "CW-20260407-0001",
		Description: "test task",
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
	assert.Equal(t, "mock", mock.Name())
}

func TestMockExecutorConfiguredResult(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.SetResult(&executor.ExecutionResult{
		Status: "failed",
		Reason: "simulated failure",
		Cost:   0.10,
		Tokens: executor.TokenUsage{PromptTokens: 500, CompletionTokens: 200},
	})

	result, err := mock.Run(context.Background(), &executor.ExecutionJob{
		TaskID: "CW-20260407-0001",
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "failed", result.Status)
	assert.Equal(t, "simulated failure", result.Reason)
	assert.Equal(t, 0.10, result.Cost)
}

func TestMockExecutorConfiguredError(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.SetError(assert.AnError)

	result, err := mock.Run(context.Background(), &executor.ExecutionJob{
		TaskID: "CW-20260407-0001",
	}, nil)

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestMockExecutorDelay(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.SetDelay(50 * time.Millisecond)

	start := time.Now()
	result, err := mock.Run(context.Background(), &executor.ExecutionJob{
		TaskID: "CW-20260407-0001",
	}, nil)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
}

func TestMockExecutorContextCancellation(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.SetDelay(5 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := mock.Run(ctx, &executor.ExecutionJob{
		TaskID: "CW-20260407-0001",
	}, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

func TestMockExecutorEmitsEvents(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.SetEvents([]executor.ExecutionEvent{
		{Type: executor.EventLog, Content: "Starting execution"},
		{Type: executor.EventProgress, Progress: float64Ptr(0.5)},
		{Type: executor.EventLog, Content: "Finishing execution"},
	})

	var events []executor.ExecutionEvent
	cb := func(event executor.ExecutionEvent) {
		events = append(events, event)
	}

	result, err := mock.Run(context.Background(), &executor.ExecutionJob{
		TaskID: "CW-20260407-0001",
	}, cb)

	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
	assert.Len(t, events, 3)
	assert.Equal(t, executor.EventLog, events[0].Type)
	assert.Equal(t, "Starting execution", events[0].Content)
	assert.Equal(t, executor.EventProgress, events[1].Type)
}

func TestMockExecutorRecordsJobs(t *testing.T) {
	mock := executor.NewMockExecutor()

	job1 := &executor.ExecutionJob{TaskID: "CW-20260407-0001"}
	job2 := &executor.ExecutionJob{TaskID: "CW-20260407-0002"}

	mock.Run(context.Background(), job1, nil)
	mock.Run(context.Background(), job2, nil)

	jobs := mock.RecordedJobs()
	assert.Len(t, jobs, 2)
	assert.Equal(t, "CW-20260407-0001", jobs[0].TaskID)
	assert.Equal(t, "CW-20260407-0002", jobs[1].TaskID)
}

func TestMockExecutorValidate(t *testing.T) {
	mock := executor.NewMockExecutor()

	err := mock.Validate(&executor.ExecutionJob{TaskID: "CW-20260407-0001"})
	assert.NoError(t, err)
}

func TestMockExecutorCapabilities(t *testing.T) {
	mock := executor.NewMockExecutor()
	caps := mock.Capabilities()
	assert.True(t, caps.SupportsStreaming)
	assert.True(t, caps.SupportsTools)
	assert.False(t, caps.SupportsSandbox)
	assert.False(t, caps.SupportsPermissions)
}

func float64Ptr(f float64) *float64 { return &f }
