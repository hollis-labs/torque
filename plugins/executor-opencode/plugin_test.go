package executoropencode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOpencode creates a temporary directory containing a fake `opencode`
// script that executes the given shell body, then prepends that directory to
// PATH so exec.Command("opencode", ...) resolves to it. The returned teardown
// func restores PATH; callers should defer it.
//
// The fake script ignores all flags passed by the executor (--agent, --dir,
// etc.) and just runs the body — sufficient for unit-testing signal parsing,
// timeout, env injection, etc.
func fakeOpencode(t *testing.T, body string) func() {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	bin := filepath.Join(dir, "opencode")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))

	orig := os.Getenv("PATH")
	t.Setenv("PATH", dir+":"+orig)
	return func() {
		t.Setenv("PATH", orig)
	}
}

// job builds a minimal valid ExecutionJob for the opencode executor.
func job(profile string) *executor.ExecutionJob {
	return &executor.ExecutionJob{
		TaskID:       "test-task-1",
		RunID:        42,
		Description:  "test description",
		AgentProfile: profile,
	}
}

// collectEvents runs the executor and collects all emitted events.
func collectEvents(t *testing.T, j *executor.ExecutionJob) (*executor.ExecutionResult, []executor.ExecutionEvent) {
	t.Helper()
	e := New()
	var events []executor.ExecutionEvent
	result, err := e.Run(context.Background(), j, func(ev executor.ExecutionEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)
	return result, events
}

// ---- Unit tests ----

func TestName(t *testing.T) {
	e := New()
	assert.Equal(t, "opencode", e.Name())
}

func TestCapabilities(t *testing.T) {
	e := New()
	caps := e.Capabilities()
	assert.True(t, caps.SupportsStreaming)
	assert.False(t, caps.SupportsTools)
	assert.False(t, caps.SupportsSandbox)
	assert.False(t, caps.SupportsPermissions)
}

func TestValidate_MissingTaskID(t *testing.T) {
	e := New()
	j := &executor.ExecutionJob{AgentProfile: "orchestrator"} // TaskID empty
	err := e.Validate(j)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TaskID")
}

func TestValidate_MissingAgentProfile(t *testing.T) {
	e := New()
	j := &executor.ExecutionJob{
		TaskID:       "CW-TEST-0001",
		AgentProfile: "",
	}
	err := e.Validate(j)
	require.Error(t, err)
	assert.True(t, executor.IsPermanent(err),
		"missing agent_profile must surface PermanentError so the scheduler blocks-no-retry")
	assert.Contains(t, err.Error(), "agent_profile")
}

func TestValidate_Valid(t *testing.T) {
	e := New()
	j := &executor.ExecutionJob{
		TaskID:       "CW-TEST-0002",
		AgentProfile: "orchestrator",
	}
	assert.NoError(t, e.Validate(j))
}

func TestValidate_BadWorkingDir(t *testing.T) {
	e := New()
	j := &executor.ExecutionJob{
		TaskID:       "CW-TEST-0003",
		AgentProfile: "orchestrator",
		WorkingDir:   "relative/path",
	}
	err := e.Validate(j)
	require.Error(t, err)
	assert.True(t, executor.IsPermanent(err))
	assert.Contains(t, err.Error(), "relative")
}

// ---- Integration-style tests using fake opencode binary ----

func TestRunDone(t *testing.T) {
	defer fakeOpencode(t, `echo "starting"
echo CLOCKWORK_DONE`)()

	j := job("orchestrator")
	result, events := collectEvents(t, j)

	assert.Equal(t, "done", result.Status)

	var sawDone bool
	for _, ev := range events {
		if ev.Type == executor.EventSignal && ev.Signal == "CLOCKWORK_DONE" {
			sawDone = true
		}
	}
	assert.True(t, sawDone, "expected CLOCKWORK_DONE signal event")
}

func TestRunReview(t *testing.T) {
	defer fakeOpencode(t, `echo CLOCKWORK_REVIEW`)()

	j := job("orchestrator")
	result, events := collectEvents(t, j)

	assert.Equal(t, "review", result.Status)

	var sawReview bool
	for _, ev := range events {
		if ev.Type == executor.EventSignal && ev.Signal == "CLOCKWORK_REVIEW" {
			sawReview = true
		}
	}
	assert.True(t, sawReview)
}

func TestRunBlocked(t *testing.T) {
	defer fakeOpencode(t, `echo "CLOCKWORK_BLOCKED: missing credentials"`)()

	j := job("orchestrator")
	result, _ := collectEvents(t, j)

	assert.Equal(t, "blocked", result.Status)
	assert.Equal(t, "missing credentials", result.Reason)
}

func TestRunSignals(t *testing.T) {
	defer fakeOpencode(t, `echo "CLOCKWORK_NOTE: progress made"
echo "CLOCKWORK_TOKENS: prompt=100 completion=50 cost=0.002"
echo CLOCKWORK_DONE`)()

	j := job("orchestrator")
	result, events := collectEvents(t, j)

	assert.Equal(t, "done", result.Status)

	var sawNote, sawTokens bool
	for _, ev := range events {
		switch ev.Type {
		case executor.EventSignal:
			if ev.Signal == "CLOCKWORK_NOTE" {
				sawNote = true
				assert.Equal(t, "progress made", ev.Content)
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
	assert.True(t, sawTokens, "expected token usage event")
	assert.Equal(t, 100, result.Tokens.PromptTokens)
	assert.Equal(t, 50, result.Tokens.CompletionTokens)
}

func TestRunTimeout(t *testing.T) {
	// Use a pure-shell busy loop so sh itself is the long-running process
	// (no child subprocess that outlives SIGTERM and keeps the pipe open).
	defer fakeOpencode(t, `while true; do :; done`)()

	e := New()
	j := job("orchestrator")
	// Apply a short deadline via the parent context rather than the
	// metadata override (override has a 60s minimum floor). The executor
	// propagates the parent context deadline into the run context.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	result, err := e.Run(ctx, j, nil)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "failed", result.Status)
	assert.Equal(t, "execution timeout", result.Reason)
	assert.Less(t, elapsed, 5*time.Second, "should complete well before the loop would finish")
}

// Exit 1 after a terminal CLOCKWORK_DONE must still succeed (matches cli executor policy).
func TestRunExitOneWithDoneSignal_IsSuccess(t *testing.T) {
	defer fakeOpencode(t, `echo CLOCKWORK_DONE
exit 1`)()

	j := job("orchestrator")
	result, err := New().Run(context.Background(), j, nil)
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status,
		"exit 1 after CLOCKWORK_DONE must still be treated as success")
}

// Exit 1 with no signal and no stdout → failure with stderr tail in Reason.
func TestRunExitOneNoSignal_FailsWithStderr(t *testing.T) {
	defer fakeOpencode(t, `echo "opencode: agent profile not found" >&2
exit 1`)()

	j := job("orchestrator")
	result, err := New().Run(context.Background(), j, nil)
	require.NoError(t, err)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Reason, "not found",
		"stderr tail must land in result.Reason")
}

// Exit 1 with no stderr and no signal → fall back to stdout tail in Reason.
func TestRunExitOneNoStderr_FallsBackToStdoutTail(t *testing.T) {
	defer fakeOpencode(t, `echo "error: model unavailable"
exit 1`)()

	j := job("orchestrator")
	result, err := New().Run(context.Background(), j, nil)
	require.NoError(t, err)
	assert.Equal(t, "failed", result.Status)
	assert.NotEmpty(t, result.Reason)
	assert.Contains(t, result.Reason, "model unavailable",
		"stdout tail must land in result.Reason when stderr is empty")
}

// CLOCKWORK_TASK_ID and CLOCKWORK_RUN_ID must be injected into the subprocess env.
func TestRunEnvInjected(t *testing.T) {
	defer fakeOpencode(t, `echo "tid=${CLOCKWORK_TASK_ID} rid=${CLOCKWORK_RUN_ID}"
echo CLOCKWORK_DONE`)()

	j := job("orchestrator")
	j.TaskID = "CW-my-task"
	j.RunID = 77

	var logLines []string
	e := New()
	_, err := e.Run(context.Background(), j, func(ev executor.ExecutionEvent) {
		if ev.Type == executor.EventLog {
			logLines = append(logLines, ev.Content)
		}
	})
	require.NoError(t, err)

	output := strings.Join(logLines, "\n")
	assert.Contains(t, output, "tid=CW-my-task")
	assert.Contains(t, output, "rid=77")
}

// Secret env vars must be stripped before the subprocess sees them.
func TestRunEnvSecretsStripped(t *testing.T) {
	defer fakeOpencode(t, `echo "key=${OPENAI_API_KEY}"
echo CLOCKWORK_DONE`)()

	j := job("orchestrator")
	j.Environment = map[string]string{"OPENAI_API_KEY": "sk-secret123"}

	var logLines []string
	_, err := New().Run(context.Background(), j, func(ev executor.ExecutionEvent) {
		if ev.Type == executor.EventLog {
			logLines = append(logLines, ev.Content)
		}
	})
	require.NoError(t, err)

	output := strings.Join(logLines, "\n")
	assert.NotContains(t, output, "sk-secret123", "secret key must be stripped from env")
}

// Stderr must be captured to a sidecar log file at $CLOCKWORK_DATA_DIR/runs/<run_id>.stderr.log.
func TestRunStderrTeedToSidecarFile(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CLOCKWORK_DATA_DIR", dataDir)

	defer fakeOpencode(t, `echo "diagnostic error" >&2
echo CLOCKWORK_DONE`)()

	j := job("orchestrator")
	j.RunID = 9999

	_, err := New().Run(context.Background(), j, nil)
	require.NoError(t, err)

	sidecar := filepath.Join(dataDir, "runs", "9999.stderr.log")
	data, readErr := os.ReadFile(sidecar)
	require.NoError(t, readErr, "expected stderr sidecar at %s", sidecar)
	assert.Contains(t, string(data), "diagnostic error")
}

// ---- buildArgs unit tests ----

func TestBuildArgs_RequiredFields(t *testing.T) {
	j := &executor.ExecutionJob{
		TaskID:       "T-1",
		AgentProfile: "orchestrator",
		Description:  "do work",
	}
	args := buildArgs(j)
	// Must contain: run --agent orchestrator <message>
	require.Contains(t, args, "run")
	require.Contains(t, args, "--agent")
	agentIdx := indexOf(args, "--agent")
	require.Greater(t, agentIdx, -1)
	assert.Equal(t, "orchestrator", args[agentIdx+1])
	// Last arg is the message
	assert.Equal(t, "do work", args[len(args)-1])
}

func TestBuildArgs_SystemPromptPrepended(t *testing.T) {
	j := &executor.ExecutionJob{
		TaskID:       "T-1",
		AgentProfile: "orchestrator",
		SystemPrompt: "You are an orchestrator.",
		Description:  "do work",
	}
	msg := buildMessage(j)
	assert.True(t, strings.HasPrefix(msg, "System: You are an orchestrator."),
		"system prompt must be prepended")
	assert.Contains(t, msg, "do work")
}

func TestBuildArgs_ModelFromMetadata(t *testing.T) {
	j := &executor.ExecutionJob{
		TaskID:       "T-1",
		AgentProfile: "orchestrator",
		Description:  "do work",
		Metadata:     map[string]any{"model": "anthropic/claude-opus-4-5"},
	}
	args := buildArgs(j)
	modelIdx := indexOf(args, "--model")
	require.Greater(t, modelIdx, -1, "--model flag must be present when metadata.model is set")
	assert.Equal(t, "anthropic/claude-opus-4-5", args[modelIdx+1])
}

func TestBuildArgs_NoModelWhenMetadataMissing(t *testing.T) {
	j := &executor.ExecutionJob{
		TaskID:       "T-1",
		AgentProfile: "orchestrator",
		Description:  "do work",
	}
	args := buildArgs(j)
	assert.NotContains(t, args, "--model", "--model must be absent when metadata.model is not set")
}

func TestBuildArgs_DirIncluded(t *testing.T) {
	j := &executor.ExecutionJob{
		TaskID:       "T-1",
		AgentProfile: "orchestrator",
		Description:  "do work",
		WorkingDir:   "/tmp/project",
	}
	args := buildArgs(j)
	dirIdx := indexOf(args, "--dir")
	require.Greater(t, dirIdx, -1, "--dir flag must be present when working_dir is set")
	assert.Equal(t, "/tmp/project", args[dirIdx+1])
}

// ---- resolveTimeout unit tests ----

func TestResolveTimeout_MetadataOverride(t *testing.T) {
	j := &executor.ExecutionJob{
		Metadata: map[string]any{"timeout_seconds_override": 120},
	}
	d := resolveTimeout(j)
	assert.Equal(t, 120*time.Second, d)
}

func TestResolveTimeout_DefaultFallback(t *testing.T) {
	j := &executor.ExecutionJob{}
	d := resolveTimeout(j)
	assert.Equal(t, 5*time.Minute, d, "default is ExecutionLimits{}.EffectiveTimeout()")
}

func TestResolveTimeout_OutOfRangeIgnored(t *testing.T) {
	j := &executor.ExecutionJob{
		Metadata: map[string]any{"timeout_seconds_override": 30}, // below 60s min
	}
	d := resolveTimeout(j)
	assert.Equal(t, 5*time.Minute, d, "out-of-range override must be ignored")
}

// ---- helpers ----

func indexOf(slice []string, target string) int {
	for i, v := range slice {
		if v == target {
			return i
		}
	}
	return -1
}
