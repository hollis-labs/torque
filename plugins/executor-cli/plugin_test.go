package executorcli

import (
	"context"
	"os"
	"path/filepath"
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
	assert.False(t, caps.SupportsSandbox)
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

// TestValidate_UnknownProfileIsPermanent covers the hot path for
// CW-20260418-0010: a task references an agent profile that isn't loaded
// AND there's no "default" profile to fall back to. The resolved profile
// has neither command nor provider set, Validate() must surface a
// PermanentError so the scheduler blocks the task rather than retry-
// exhausting it, and the profile name the user gave must appear in the
// error so the blocked_reason is actionable.
func TestValidate_UnknownProfileIsPermanent(t *testing.T) {
	// profile "default" explicitly absent so GetProfileOrDefault returns zero.
	pm := config.ProfileMap{"other": config.AgentProfile{Command: "sh"}}
	e := New(pm)
	j := &executor.ExecutionJob{
		TaskID:       "CW-TEST-0001",
		AgentProfile: "nanite-frontend", // not in pm, and no "default" either
	}
	err := e.Validate(j)
	require.Error(t, err)
	assert.True(t, executor.IsPermanent(err),
		"unknown/unloaded profile must surface PermanentError so the scheduler blocks-no-retry")
	assert.Contains(t, err.Error(), "nanite-frontend",
		"error must name the user-supplied profile so blocked_reason is actionable")
	assert.Contains(t, err.Error(), "neither command nor provider")
}

// TestValidate_EmptyProfileIsPermanent covers the agent_profile='' variant.
// With no profile name and no "default" profile, the resolved profile is
// the zero value — same failure shape, same PermanentError treatment.
func TestValidate_EmptyProfileIsPermanent(t *testing.T) {
	pm := config.ProfileMap{} // deliberately empty, no "default"
	e := New(pm)
	j := &executor.ExecutionJob{
		TaskID:       "CW-TEST-0002",
		AgentProfile: "",
	}
	err := e.Validate(j)
	require.Error(t, err)
	assert.True(t, executor.IsPermanent(err),
		"empty agent_profile with no default profile must surface PermanentError")
}

// TestValidate_ProfilePresentNotPermanent confirms a valid profile does NOT
// produce a PermanentError — we don't want to accidentally classify normal
// transient failures as permanent.
func TestValidate_ProfilePresentNotPermanent(t *testing.T) {
	pm := profiles("default", config.AgentProfile{Command: "sh"})
	e := New(pm)
	j := job("default")
	assert.NoError(t, e.Validate(j))
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

func TestCLIExecutorArtifactPopulatesResult(t *testing.T) {
	script := `echo "starting work"
echo '{"signal": "CLOCKWORK_ARTIFACT", "type": "diff", "content": "--- a/x"}'
echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	results, events := collectEvents(t, e, j)
	result := results[0]

	assert.Equal(t, "done", result.Status)
	require.Len(t, result.Artifacts, 1)
	assert.Equal(t, "diff", result.Artifacts[0].Type)
	assert.Equal(t, "--- a/x", result.Artifacts[0].Content)

	var sawArtifactEvent bool
	for _, ev := range events {
		if ev.Type == executor.EventArtifact {
			sawArtifactEvent = true
			require.NotNil(t, ev.Artifact)
			assert.Equal(t, "diff", ev.Artifact.Type)
			assert.Equal(t, "--- a/x", ev.Artifact.Content)
		}
	}
	assert.True(t, sawArtifactEvent, "expected EventArtifact to be emitted via cb")
}

func TestCLIExecutorMultipleArtifacts(t *testing.T) {
	script := `echo '{"signal": "CLOCKWORK_ARTIFACT", "type": "diff", "content": "first"}'
echo '{"signal": "CLOCKWORK_ARTIFACT", "type": "log", "content": "second"}'
echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	results, _ := collectEvents(t, e, j)
	result := results[0]

	assert.Equal(t, "done", result.Status)
	require.Len(t, result.Artifacts, 2)
	assert.Equal(t, "diff", result.Artifacts[0].Type)
	assert.Equal(t, "first", result.Artifacts[0].Content)
	assert.Equal(t, "log", result.Artifacts[1].Type)
	assert.Equal(t, "second", result.Artifacts[1].Content)
}

func TestCLIExecutorMalformedArtifactFallsBackToLog(t *testing.T) {
	script := `echo '{"signal": "CLOCKWORK_ARTIFACT"}'
echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	results, events := collectEvents(t, e, j)
	result := results[0]

	assert.Equal(t, "done", result.Status)
	assert.Empty(t, result.Artifacts, "malformed artifact must not be appended")

	var sawLog bool
	for _, ev := range events {
		if ev.Type == executor.EventLog && strings.Contains(ev.Content, "CLOCKWORK_ARTIFACT") {
			sawLog = true
		}
	}
	assert.True(t, sawLog, "expected malformed artifact to fall back to a LogEvent")
}

// Bug CW-20260417-0025: when the agent emits a valid CLOCKWORK_DONE on its
// own line but the process exits with status 1 (claude tends to do this
// after a successful run), the run must still be marked done. Exit code is
// a weak signal relative to the structured completion marker.
func TestRunPrintMode_ExitOneWithDoneSignal_IsSuccess(t *testing.T) {
	// sh script that emits CLOCKWORK_DONE then exits 1.
	script := `echo CLOCKWORK_DONE
exit 1`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	result, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status,
		"exit 1 after a CLOCKWORK_DONE signal must still be treated as success")
}

// Exit 1 with no terminal signal and no stdout → true failure. The Reason
// field must carry the stderr tail so callers can diagnose.
func TestRunPrintMode_ExitOneNoSignal_FailsWithStderr(t *testing.T) {
	// sh script that writes a diagnostic to stderr and exits 1.
	script := `echo "claude: usage error: --output-format stream-json requires --verbose" >&2
exit 1`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	result, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err, "failures produce a failed result, not a Go error")
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Reason, "requires --verbose",
		"stderr tail must land in result.Reason so scheduler can persist it")
}

// Stderr from the child process must be captured to a sidecar file at
// $CLOCKWORK_DATA_DIR/runs/<run_id>.stderr.log so we can diagnose failures
// after the fact (Bug CW-20260417-0024 relevance).
func TestRunPrintMode_StderrTeedToSidecarFile(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CLOCKWORK_DATA_DIR", dataDir)

	script := `echo "diagnostic: something went sideways" >&2
echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")
	j.RunID = 4242

	_, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err)

	sidecar := filepath.Join(dataDir, "runs", "4242.stderr.log")
	data, readErr := os.ReadFile(sidecar)
	require.NoError(t, readErr, "expected stderr sidecar at %s", sidecar)
	assert.Contains(t, string(data), "diagnostic: something went sideways")
}

// When the agent emits CLOCKWORK_DONE on its own line but exits 0 normally,
// the existing happy path must keep working (regression guard for the
// exit-code-tolerant branch).
func TestRunPrintMode_ExitZeroWithDoneSignal_StillSucceeds(t *testing.T) {
	script := `echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	result, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
}

// Stream-json profile variant: helper constructs a profile that turns on
// the stream-json parser but still executes via sh so we can feed canned
// NDJSON via stdout.
func streamShellProfile(script string) config.AgentProfile {
	return config.AgentProfile{
		Command:        "sh",
		Args:           []string{"-c", script},
		OutputFormat:   "stream-json",
		TimeoutSeconds: 10,
	}
}

// Stream-json path: exit 1 but the result event carried status=done → success.
func TestRunStreamJSON_ExitOneWithResultEvent_IsSuccess(t *testing.T) {
	// Emit a valid result event then exit 1.
	script := `cat <<'JSON'
{"type":"content_block_delta","delta":{"type":"text_delta","text":"working\n"}}
{"type":"result","result":"{\"status\":\"done\",\"signal\":\"CLOCKWORK_DONE\",\"summary\":\"ok\"}","input_tokens":10,"output_tokens":5,"cost_usd":0.001}
JSON
exit 1`
	pm := profiles("default", streamShellProfile(script))
	e := New(pm)
	j := job("default")

	result, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
}

// Stream-json path: no result event, but the mid-stream content carried
// CLOCKWORK_DONE on its own text_delta line → fallback parse recovers.
func TestRunStreamJSON_FallbackRecoversDoneFromDelta(t *testing.T) {
	script := `cat <<'JSON'
{"type":"content_block_delta","delta":{"type":"text_delta","text":"ran tests\nCLOCKWORK_DONE\n"}}
JSON`
	pm := profiles("default", streamShellProfile(script))
	e := New(pm)
	j := job("default")

	result, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status,
		"CLOCKWORK_DONE in a content_block_delta must still succeed when no result event arrived")
}

// Bug CW-20260417-0031: when a run fails with zero stderr (claude --print
// writes everything to stdout), result.Reason must still carry a
// diagnostic — fall back to the stdout tail so the scheduler can persist
// something non-empty as runs.ErrorMessage and operators have a signal.
func TestRunPrintMode_ExitOneNoStderr_FallsBackToStdoutTail(t *testing.T) {
	// Script writes only to stdout, emits no terminal signal, exits 1.
	script := `echo "error: claude model request failed (rate limited)"
echo "retry after 60s"
exit 1`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")

	result, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err, "failures produce a failed result, not a Go error")
	assert.Equal(t, "failed", result.Status)
	assert.NotEmpty(t, result.Reason, "Reason must never be empty on failure")
	assert.Contains(t, result.Reason, "rate limited",
		"stdout tail must land in result.Reason when stderr is empty")
}

// Stream-json path variant: no result event, no signal, exit 1, and the
// agent printed diagnostic text via content_block_delta (which accumulates
// into the fallback buffer) — Reason must surface that text.
func TestRunStreamJSON_ExitOneNoStderr_FallsBackToStdoutTail(t *testing.T) {
	// Emit a text_delta with diagnostic content but no result event.
	script := `cat <<'JSON'
{"type":"content_block_delta","delta":{"type":"text_delta","text":"error: model unavailable\nplease retry later\n"}}
JSON
exit 1`
	pm := profiles("default", streamShellProfile(script))
	e := New(pm)
	j := job("default")

	result, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err)
	assert.Equal(t, "failed", result.Status)
	assert.NotEmpty(t, result.Reason)
	assert.Contains(t, result.Reason, "model unavailable",
		"stream-json stdout tail must land in result.Reason when stderr is empty")
}

// Stream-json path: no result event, no signal, exit 1 → genuine failure.
// Stderr tail must land in result.Reason.
func TestRunStreamJSON_ExitOneNoResultNoSignal_FailsWithStderr(t *testing.T) {
	script := `echo "claude: token expired" >&2
exit 1`
	pm := profiles("default", streamShellProfile(script))
	e := New(pm)
	j := job("default")

	result, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Reason, "token expired")
}

// CW-20260417-0036: when the agent finishes via stream-json with a
// schema-conformant result event, every path in files_changed must be
// auto-converted into an Artifact (classified by extension) and emitted
// via cb. Schema mode suppresses freeform CLOCKWORK_ARTIFACT lines, so
// without this conversion the deliverable-check gate sees zero artifacts
// and erroneously blocks done runs.
func TestRunStreamJSON_FilesChangedBecomeArtifacts(t *testing.T) {
	script := `cat <<'JSON'
{"type":"result","result":"{\"status\":\"done\",\"signal\":\"CLOCKWORK_DONE\",\"summary\":\"ok\",\"files_changed\":[\"src/foo.go\",\"docs/screenshots/before.png\",\"docs/screenshots/after.JPG\"]}"}
JSON`
	pm := profiles("default", streamShellProfile(script))
	e := New(pm)
	j := job("default")

	results, events := collectEvents(t, e, j)
	result := results[0]

	assert.Equal(t, "done", result.Status)
	require.Len(t, result.Artifacts, 3, "every files_changed entry must become an artifact")

	byPath := map[string]executor.Artifact{}
	for _, a := range result.Artifacts {
		byPath[a.FilePath] = a
	}
	require.Contains(t, byPath, "src/foo.go")
	require.Contains(t, byPath, "docs/screenshots/before.png")
	require.Contains(t, byPath, "docs/screenshots/after.JPG")
	assert.Equal(t, "diff", byPath["src/foo.go"].Type)
	assert.Equal(t, "screenshot", byPath["docs/screenshots/before.png"].Type)
	assert.Equal(t, "screenshot", byPath["docs/screenshots/after.JPG"].Type,
		"extension classification must be case-insensitive")
	for _, a := range result.Artifacts {
		assert.Equal(t, "agent-auto", a.Metadata["origin"],
			"auto-emitted artifacts must carry origin=agent-auto")
	}

	var artifactEvents int
	for _, ev := range events {
		if ev.Type == executor.EventArtifact {
			artifactEvents++
		}
	}
	assert.Equal(t, 3, artifactEvents,
		"each auto-classified artifact must be dispatched via cb so the scheduler persists it")
}

// Manual CLOCKWORK_ARTIFACT signals must not be duplicated by the
// files_changed auto-emitter. Dedupe key is FilePath.
func TestRunStreamJSON_FilesChangedDedupesAgainstManualArtifact(t *testing.T) {
	script := `cat <<'JSON'
{"type":"content_block_delta","delta":{"type":"text_delta","text":"{\"signal\":\"CLOCKWORK_ARTIFACT\",\"type\":\"diff\",\"file_path\":\"src/foo.go\",\"content\":\"manual\"}\n"}}
{"type":"result","result":"{\"status\":\"done\",\"signal\":\"CLOCKWORK_DONE\",\"files_changed\":[\"src/foo.go\",\"src/bar.go\"]}"}
JSON`
	pm := profiles("default", streamShellProfile(script))
	e := New(pm)
	j := job("default")

	results, _ := collectEvents(t, e, j)
	result := results[0]

	assert.Equal(t, "done", result.Status)
	require.Len(t, result.Artifacts, 2, "manual artifact + one new auto-classified one (dedupe by file_path)")

	var manualKept, autoNew bool
	for _, a := range result.Artifacts {
		if a.FilePath == "src/foo.go" {
			manualKept = true
			assert.Equal(t, "manual", a.Content, "manual artifact must win over auto-emit")
			assert.Nil(t, a.Metadata, "manual artifact must not be tagged agent-auto")
		}
		if a.FilePath == "src/bar.go" {
			autoNew = true
			assert.Equal(t, "diff", a.Type)
			assert.Equal(t, "agent-auto", a.Metadata["origin"])
		}
	}
	assert.True(t, manualKept, "manual artifact must remain")
	assert.True(t, autoNew, "new path must be auto-classified")
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
