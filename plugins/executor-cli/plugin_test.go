package executorcli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoProfile creates a profile using /bin/sh with a custom shell script as the
// command. We override Command so buildCommandSpec uses sh.
func shellProfile(script string) config.AgentProfile {
	return config.AgentProfile{
		Command:        "sh",
		Args:           []string{"-c", script},
		OutputFormat:   "print",
		TimeoutSeconds: 10,
	}
}

func profiles(name string, p config.AgentProfile) config.ProfileMap {
	return config.ProfileMap{name: p}
}

// job builds a minimal valid ExecutionJob.
func job(profileName string) *executor.ExecutionJob {
	return &executor.ExecutionJob{
		TaskID:       "test-task-1",
		RunID:        42,
		Description:  "test description",
		AgentProfile: profileName,
	}
}

// collectEvents runs the executor and collects all emitted events.
func collectEvents(t *testing.T, e *CLIExecutor, j *executor.ExecutionJob) ([]*executor.ExecutionResult, []executor.ExecutionEvent) {
	t.Helper()
	var events []executor.ExecutionEvent
	result, err := e.Run(context.Background(), j, func(ev executor.ExecutionEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)
	return []*executor.ExecutionResult{result}, events
}

// ---- Tests ----

func TestName(t *testing.T) {
	e := New(nil)
	assert.Equal(t, "cli", e.Name())
}

func TestCapabilities(t *testing.T) {
	e := New(nil)
	caps := e.Capabilities()
	assert.True(t, caps.SupportsStreaming)
	assert.False(t, caps.SupportsTools)
	assert.True(t, caps.SupportsSandbox)
	assert.False(t, caps.SupportsPermissions)
}

func TestValidate_Valid(t *testing.T) {
	pm := profiles("default", config.AgentProfile{Command: "sh"})
	e := New(pm)
	j := job("default")
	assert.NoError(t, e.Validate(j))
}

func TestValidate_MissingTaskID(t *testing.T) {
	pm := profiles("default", config.AgentProfile{Command: "sh"})
	e := New(pm)
	j := &executor.ExecutionJob{AgentProfile: "default"} // TaskID empty
	err := e.Validate(j)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TaskID")
}

func TestValidate_MissingCommandAndProvider(t *testing.T) {
	pm := profiles("default", config.AgentProfile{})
	e := New(pm)
	j := job("default")
	err := e.Validate(j)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "neither command nor provider")
}

func TestRunWithEcho(t *testing.T) {
	script := `echo "hello world"
echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	results, events := collectEvents(t, e, j)
	result := results[0]

	assert.Equal(t, "done", result.Status)

	var logLines []string
	for _, ev := range events {
		if ev.Type == executor.EventLog {
			logLines = append(logLines, ev.Content)
		}
	}
	assert.Contains(t, logLines, "hello world")
}

func TestRunWithSignals(t *testing.T) {
	script := `echo "starting work"
echo "CLOCKWORK_NOTE: progress made"
echo "CLOCKWORK_TOKENS: prompt=100 completion=50 cost=0.002"
echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	results, events := collectEvents(t, e, j)
	result := results[0]

	assert.Equal(t, "done", result.Status)

	var sawNote, sawTokens, sawDone bool
	for _, ev := range events {
		switch ev.Type {
		case executor.EventSignal:
			switch ev.Signal {
			case "CLOCKWORK_NOTE":
				sawNote = true
				assert.Equal(t, "progress made", ev.Content)
			case "CLOCKWORK_DONE":
				sawDone = true
			}
		case executor.EventTokenUsage:
			sawTokens = true
			require.NotNil(t, ev.Tokens)
			assert.Equal(t, 100, ev.Tokens.PromptTokens)
			assert.Equal(t, 50, ev.Tokens.CompletionTokens)
			assert.InDelta(t, 0.002, ev.Tokens.Cost, 0.0001)
		}
	}

	assert.True(t, sawNote, "expected CLOCKWORK_NOTE event")
	assert.True(t, sawTokens, "expected token event")
	assert.True(t, sawDone, "expected CLOCKWORK_DONE event")
	assert.Equal(t, 100, result.Tokens.PromptTokens)
	assert.Equal(t, 50, result.Tokens.CompletionTokens)
}

func TestRunBlocked(t *testing.T) {
	script := `echo "CLOCKWORK_BLOCKED: missing credentials"`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	results, events := collectEvents(t, e, j)
	result := results[0]

	assert.Equal(t, "blocked", result.Status)
	assert.Equal(t, "missing credentials", result.Reason)

	var sawBlocked bool
	for _, ev := range events {
		if ev.Type == executor.EventSignal && ev.Signal == "CLOCKWORK_BLOCKED" {
			sawBlocked = true
			assert.Equal(t, "missing credentials", ev.Content)
		}
	}
	assert.True(t, sawBlocked, "expected CLOCKWORK_BLOCKED event")
}

func TestRunReview(t *testing.T) {
	script := `echo CLOCKWORK_REVIEW`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	results, events := collectEvents(t, e, j)
	result := results[0]

	assert.Equal(t, "review", result.Status)

	var sawReview bool
	for _, ev := range events {
		if ev.Type == executor.EventSignal && ev.Signal == "CLOCKWORK_REVIEW" {
			sawReview = true
		}
	}
	assert.True(t, sawReview, "expected CLOCKWORK_REVIEW event")
}

func TestRunTimeout(t *testing.T) {
	script := `sleep 60`
	pm := profiles("default", config.AgentProfile{
		Command:        "sh",
		Args:           []string{"-c", script},
		OutputFormat:   "print",
		TimeoutSeconds: 1,
	})
	e := New(pm)
	j := job("default")

	ctx := context.Background()
	start := time.Now()
	result, err := e.Run(ctx, j, nil)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "failed", result.Status)
	assert.Equal(t, "execution timeout", result.Reason)
	// Should complete well under the sleep duration.
	assert.Less(t, elapsed, 10*time.Second)
}

func TestRunEnvFiltered(t *testing.T) {
	// Script that prints the value of OPENAI_API_KEY — should be empty after filtering.
	script := `echo "key=${OPENAI_API_KEY}"`
	pm := profiles("default", shellProfile(script))
	e := New(pm)

	j := job("default")
	j.Environment = map[string]string{
		"OPENAI_API_KEY": "sk-secret123",
	}

	var logLines []string
	_, err := e.Run(context.Background(), j, func(ev executor.ExecutionEvent) {
		if ev.Type == executor.EventLog {
			logLines = append(logLines, ev.Content)
		}
	})
	require.NoError(t, err)

	// The key should have been stripped by FilterEnv before the shell sees it.
	output := strings.Join(logLines, "\n")
	assert.NotContains(t, output, "sk-secret123", "secret key must be stripped from env")
}

func TestRunTaskIDAndRunIDInjected(t *testing.T) {
	script := `echo "tid=${CLOCKWORK_TASK_ID} rid=${CLOCKWORK_RUN_ID}"`
	pm := profiles("default", shellProfile(script))
	e := New(pm)

	j := job("default")
	j.TaskID = "my-task"
	j.RunID = 99

	var logLines []string
	_, err := e.Run(context.Background(), j, func(ev executor.ExecutionEvent) {
		if ev.Type == executor.EventLog {
			logLines = append(logLines, ev.Content)
		}
	})
	require.NoError(t, err)

	output := strings.Join(logLines, "\n")
	assert.Contains(t, output, "tid=my-task")
	assert.Contains(t, output, "rid=99")
}
