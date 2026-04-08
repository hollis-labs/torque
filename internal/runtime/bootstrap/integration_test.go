package bootstrap_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/bootstrap"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_CLIExecutorDispatch(t *testing.T) {
	// Create a script that simulates a full agent execution
	dir := t.TempDir()
	script := filepath.Join(dir, "agent.sh")
	content := `#!/bin/sh
echo "Analyzing task..."
echo "CLOCKWORK_NOTE: Found the root cause in auth.go"
echo '{"signal": "CLOCKWORK_PROGRESS", "progress": 0.5, "message": "halfway done"}'
echo "CLOCKWORK_TOKENS: prompt=200 completion=100 cost=0.02"
echo '{"signal": "CLOCKWORK_ARTIFACT", "type": "diff", "content": "+fixed"}'
echo "CLOCKWORK_DONE"
`
	require.NoError(t, os.WriteFile(script, []byte(content), 0755))

	profiles := config.ProfileMap{
		"test-agent": {
			Executor:       "cli",
			Provider:       "generic",
			Command:        "sh",
			Args:           []string{script},
			OutputFormat:   "print",
			TimeoutSeconds: 10,
		},
	}

	// Bootstrap registry
	reg := executor.NewRegistry()
	require.NoError(t, bootstrap.Executors(reg, profiles, nil))

	// Look up the CLI executor
	exec, err := reg.Get("cli")
	require.NoError(t, err)

	// Build the job
	job := &executor.ExecutionJob{
		TaskID:       "CW-20260407-0001",
		RunID:        1,
		Description:  "Fix the authentication bug in auth.go",
		WorkingDir:   dir,
		AgentProfile: "test-agent",
	}

	// Validate
	require.NoError(t, exec.Validate(job))

	// Execute
	var events []executor.ExecutionEvent
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := exec.Run(ctx, job, func(ev executor.ExecutionEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)

	// Verify result
	assert.Equal(t, "done", result.Status)
	assert.Equal(t, 200, result.Tokens.PromptTokens)
	assert.Equal(t, 100, result.Tokens.CompletionTokens)
	assert.InDelta(t, 0.02, result.Cost, 0.001)

	// Verify events were emitted
	eventTypes := make(map[executor.EventType]int)
	signalTypes := make(map[string]int)
	for _, ev := range events {
		eventTypes[ev.Type]++
		if ev.Signal != "" {
			signalTypes[ev.Signal]++
		}
	}

	assert.Greater(t, eventTypes[executor.EventLog], 0, "should have log events")
	assert.Greater(t, eventTypes[executor.EventSignal], 0, "should have signal events")
	assert.Greater(t, signalTypes["CLOCKWORK_NOTE"], 0, "should have note signal")
	assert.Greater(t, signalTypes["CLOCKWORK_DONE"], 0, "should have done signal")
}

func TestIntegration_ExecutorRegistryDispatch(t *testing.T) {
	profiles := config.ProfileMap{
		"default": {
			Executor:       "cli",
			Provider:       "generic",
			Command:        "sh",
			Args:           []string{"-c", "echo CLOCKWORK_DONE"},
			OutputFormat:   "print",
			TimeoutSeconds: 5,
		},
	}

	reg := executor.NewRegistry()
	require.NoError(t, bootstrap.Executors(reg, profiles, nil))

	// Simulate what the scheduler does: look up executor by name
	job := &executor.ExecutionJob{
		TaskID:      "CW-20260407-0001",
		Description: "Quick test",
		WorkingDir:  os.TempDir(),
	}

	// The task specifies executor: "cli"
	executorName := "cli"
	exec, err := reg.Get(executorName)
	require.NoError(t, err, "executor %q should be registered", executorName)

	require.NoError(t, exec.Validate(job))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := exec.Run(ctx, job, func(ev executor.ExecutionEvent) {})
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
}

func TestIntegration_AllSignalTypes(t *testing.T) {
	// Create a script that emits every signal type
	dir := t.TempDir()
	script := filepath.Join(dir, "all-signals.sh")
	content := `#!/bin/sh
echo "Starting execution"
echo "CLOCKWORK_NOTE: beginning analysis"
echo "CLOCKWORK_TASK: Add error handling"
echo "CLOCKWORK_TOKENS: prompt=500 completion=250 cost=0.03"
echo '{"signal": "CLOCKWORK_CHECKPOINT", "label": "tests_passing"}'
echo '{"signal": "CLOCKWORK_PROGRESS", "progress": 0.5}'
echo '{"signal": "CLOCKWORK_SUBTASK", "title": "Fix handler", "status": "done"}'
echo '{"signal": "CLOCKWORK_ARTIFACT", "type": "diff", "content": "+fixed"}'
echo "CLOCKWORK_REVIEW"
`
	require.NoError(t, os.WriteFile(script, []byte(content), 0755))

	profiles := config.ProfileMap{
		"signal-test": {
			Executor:       "cli",
			Provider:       "generic",
			Command:        "sh",
			Args:           []string{script},
			OutputFormat:   "print",
			TimeoutSeconds: 5,
		},
	}

	reg := executor.NewRegistry()
	require.NoError(t, bootstrap.Executors(reg, profiles, nil))

	exec, err := reg.Get("cli")
	require.NoError(t, err)

	job := &executor.ExecutionJob{
		TaskID:       "CW-20260407-0001",
		Description:  "Test all signals",
		WorkingDir:   dir,
		AgentProfile: "signal-test",
	}

	var signals []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := exec.Run(ctx, job, func(ev executor.ExecutionEvent) {
		if ev.Signal != "" {
			signals = append(signals, ev.Signal)
		}
	})
	require.NoError(t, err)
	assert.Equal(t, "review", result.Status)

	assert.Contains(t, signals, "CLOCKWORK_NOTE")
	assert.Contains(t, signals, "CLOCKWORK_TASK")
	assert.Contains(t, signals, "CLOCKWORK_CHECKPOINT")
	assert.Contains(t, signals, "CLOCKWORK_PROGRESS")
	assert.Contains(t, signals, "CLOCKWORK_SUBTASK")
	assert.Contains(t, signals, "CLOCKWORK_ARTIFACT")
	assert.Contains(t, signals, "CLOCKWORK_REVIEW")
}
