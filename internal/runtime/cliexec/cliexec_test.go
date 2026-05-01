package cliexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdapter is a minimal go-providers CLIAdapter for in-package testing.
// The harness writes a small shell script that emits one event per line:
//
//	delta:<text>     → EventDelta with Content=<text>
//	usage:<in>:<out> → EventUsage with InputTokens=in, OutputTokens=out
//	done             → EventDone (terminal)
//	error:<text>     → EventError (terminal)
//
// fakeAdapter.BuildArgs returns nothing; the script ignores argv.
type fakeAdapter struct {
	binary string
}

func (a *fakeAdapter) Name() string { return "fake-cliexec" }

func (a *fakeAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	return nil
}

func (a *fakeAdapter) ParseLine(line []byte) ([]provider.StreamEvent, error) {
	s := strings.TrimRight(string(line), "\r\n")
	switch {
	case strings.HasPrefix(s, "delta:"):
		return []provider.StreamEvent{{
			Type:    provider.EventDelta,
			Content: strings.TrimPrefix(s, "delta:"),
		}}, nil
	case strings.HasPrefix(s, "usage:"):
		var in, out int
		_, err := fmtScanf(strings.TrimPrefix(s, "usage:"), &in, &out)
		if err != nil {
			return nil, nil
		}
		return []provider.StreamEvent{{
			Type:  provider.EventUsage,
			Usage: &provider.Usage{InputTokens: in, OutputTokens: out, StopReason: "end_turn"},
		}}, nil
	case s == "done":
		return []provider.StreamEvent{{Type: provider.EventDone}}, nil
	case strings.HasPrefix(s, "error:"):
		return []provider.StreamEvent{{
			Type:  provider.EventError,
			Error: strings.TrimPrefix(s, "error:"),
		}}, nil
	}
	return nil, nil
}

func (a *fakeAdapter) Detect() (string, bool) { return a.binary, a.binary != "" }

// fmtScanf is a tiny stand-in for fmt.Sscanf to avoid pulling fmt only for
// the test-side integer parsing.
func fmtScanf(s string, a, b *int) (int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, errors.New("expected in:out")
	}
	in, err := parseInt(parts[0])
	if err != nil {
		return 0, err
	}
	out, err := parseInt(parts[1])
	if err != nil {
		return 0, err
	}
	*a = in
	*b = out
	return 2, nil
}

func parseInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not int")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// writeFakeCLI drops a tiny shell script in dir that emits the lines verbatim
// then exits with the given code. Returns the absolute path. Skips on Windows
// since the script needs sh.
func writeFakeCLI(t *testing.T, dir string, lines []string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test script needs sh; not running on Windows")
	}
	path := filepath.Join(dir, "fake-cli.sh")
	body := "#!/bin/sh\n"
	for _, l := range lines {
		body += "printf '%s\\n' " + shellQuote(l) + "\n"
	}
	body += "exit " + parseExit(exitCode) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	return path
}

func parseExit(code int) string {
	if code < 0 || code > 9 {
		return "0"
	}
	return string(rune('0' + code))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// runCliexecWithFakeAdapter wires a CLIExecutor manually around a fakeAdapter
// + ExecutionJob and returns (result, err, captured-events). Bypasses the
// profile / adapterFor switch so test output is not gated on real CLI binaries.
func runCliexecWithFakeAdapter(
	t *testing.T,
	ctx context.Context,
	job *executor.ExecutionJob,
	adapter provider.CLIAdapter,
) (*executor.ExecutionResult, error, []executor.ExecutionEvent) {
	t.Helper()

	// Stand in for the cliexec.Run plumbing. We construct the runtime + manager
	// directly so we can use a fake adapter without registering it in adapterFor.
	rt, err := agentsessions.NewFromAdapter(agentsessions.AdapterRuntimeConfig{
		ID:      "clockwork-cli/test/" + adapter.Name(),
		Kind:    "cli",
		Adapter: adapter,
		Caps:    agentsessions.Capabilities{BinaryRequired: true},
	})
	require.NoError(t, err)

	stderrWriter, stderrTail, closeStderr := openStderrSidecar(job.RunID)
	defer closeStderr()

	eventFanout := make(chan provider.StreamEvent, 64)
	mgr := agentsessions.NewManager(nil)

	sessID := "test-cliexec"
	startErr := mgr.Start(ctx, agentsessions.StartRequest{
		ID:      sessID,
		Runtime: rt,
		Options: agentsessions.StartOptions{
			Workdir:     job.WorkingDir,
			EventFanout: eventFanout,
			Stderr:      stderrWriter,
		},
	})
	require.NoError(t, startErr)

	var (
		captured  []executor.ExecutionEvent
		streamErr error
	)
	cb := func(ev executor.ExecutionEvent) { captured = append(captured, ev) }

	result := &executor.ExecutionResult{}

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		for ev := range eventFanout {
			translateStreamEvent(ev, result, cb, func(s string) {
				if streamErr == nil {
					streamErr = errors.New(s)
				}
			})
		}
	}()

	sendErr := mgr.SendInput(sessID, []byte(job.Description))
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	_ = mgr.Stop(stopCtx, sessID)

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	exitCode, _ := mgr.WaitSession(waitCtx, sessID)

	close(eventFanout)
	<-doneCh

	result.Cost = result.Tokens.Cost

	switch {
	case sendErr != nil:
		result.Status = "failed"
		result.Reason = failureReason(stderrTail, sendErr, streamErr)
	case streamErr != nil && exitCode != 0:
		result.Status = "failed"
		result.Reason = streamErr.Error()
	case exitCode == 0:
		result.Status = "done"
	default:
		result.Status = "failed"
		result.Reason = failureReason(stderrTail, errors.New("nonzero exit"), streamErr)
	}

	return result, nil, captured
}

func TestCliexec_CleanExitMapsToDone(t *testing.T) {
	dir := t.TempDir()
	script := writeFakeCLI(t, dir, []string{"delta:hi", "usage:100:50", "done"}, 0)

	job := &executor.ExecutionJob{
		TaskID:      "CW-TEST-0001",
		RunID:       1,
		Description: "test prompt",
		WorkingDir:  dir,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err, events := runCliexecWithFakeAdapter(t, ctx, job, &fakeAdapter{binary: script})
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status, "clean exit (code 0) maps to done; lifecycle manager applies OnDone")
	assert.Equal(t, 100, result.Tokens.PromptTokens)
	assert.Equal(t, 50, result.Tokens.CompletionTokens)

	// At least one delta surfaced as a log event, and one usage event surfaced.
	var sawLog, sawTokens bool
	for _, ev := range events {
		switch ev.Type {
		case executor.EventLog:
			if strings.Contains(ev.Content, "hi") {
				sawLog = true
			}
		case executor.EventTokenUsage:
			sawTokens = true
		}
	}
	assert.True(t, sawLog, "expected delta to surface as EventLog")
	assert.True(t, sawTokens, "expected usage to surface as EventTokenUsage")
}

func TestCliexec_NonzeroExitMapsToFailed(t *testing.T) {
	dir := t.TempDir()
	script := writeFakeCLI(t, dir, []string{"delta:partial work"}, 1)

	job := &executor.ExecutionJob{
		TaskID:      "CW-TEST-0002",
		RunID:       2,
		Description: "test prompt",
		WorkingDir:  dir,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err, _ := runCliexecWithFakeAdapter(t, ctx, job, &fakeAdapter{binary: script})
	require.NoError(t, err)
	assert.Equal(t, "failed", result.Status, "nonzero exit without terminal signal → failed")
}

func TestCliexec_ProviderErrorEventCapturedAsReason(t *testing.T) {
	dir := t.TempDir()
	// Adapter emits an error event then the script exits nonzero.
	script := writeFakeCLI(t, dir, []string{"error:auth token expired"}, 2)

	job := &executor.ExecutionJob{
		TaskID:      "CW-TEST-0003",
		RunID:       3,
		Description: "test prompt",
		WorkingDir:  dir,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err, _ := runCliexecWithFakeAdapter(t, ctx, job, &fakeAdapter{binary: script})
	require.NoError(t, err)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Reason, "auth token expired",
		"provider EventError text should be captured into result.Reason")
}

func TestAdapterFor_KnownProvidersResolve(t *testing.T) {
	cases := []string{"claude", "codex", "gemini", "copilot", "opencode"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			a, caps, err := adapterFor(config.AgentProfile{Provider: name}, "test-profile")
			require.NoError(t, err)
			require.NotNil(t, a)
			assert.Equal(t, name, a.Name())
			assert.True(t, caps.BinaryRequired, "all CLI adapters require their binary")
		})
	}
}

func TestAdapterFor_RejectsUnknown(t *testing.T) {
	_, _, err := adapterFor(config.AgentProfile{Provider: "made-up"}, "test-profile")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider")

	_, _, err = adapterFor(config.AgentProfile{Provider: ""}, "test-profile")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty provider")
}

func TestAdapterFor_OpencodeRequiresProfileName(t *testing.T) {
	// opencode --agent value defaults to the clockwork profile lookup name;
	// an empty profile name is a permanent config error.
	_, _, err := adapterFor(config.AgentProfile{Provider: "opencode"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "opencode provider requires task.agent_profile")
}

func TestAdapterFor_OpencodeAgentSetFromProfileName(t *testing.T) {
	a, _, err := adapterFor(config.AgentProfile{Provider: "opencode"}, "clockwork-backend")
	require.NoError(t, err)
	oc, ok := a.(*provider.OpencodeAdapter)
	require.True(t, ok, "expected *provider.OpencodeAdapter, got %T", a)
	assert.Equal(t, "clockwork-backend", oc.Agent,
		"OpencodeAdapter.Agent should be populated from the clockwork profile name")
	// Model is left zero — cliexec wrapper appends --model from profile.Model
	// uniformly across adapters; setting it here would duplicate the flag.
	assert.Empty(t, oc.Model, "Model should be left for the cliexec wrapper to append")
}

func TestAdapterFor_ClaudeSkipPermissionsHonored(t *testing.T) {
	plain, _, err := adapterFor(config.AgentProfile{Provider: "claude"}, "test-profile")
	require.NoError(t, err)

	dev, _, err := adapterFor(config.AgentProfile{
		Provider: "claude",
		Args:     []string{"--dangerously-skip-permissions"},
	}, "test-profile")
	require.NoError(t, err)

	// Both are ClaudeAdapter; one has SkipPermissions=true. The skip flag is
	// observable in the BuildArgs output.
	plainArgs := plain.BuildArgs("hi", "", "")
	devArgs := dev.BuildArgs("hi", "", "")

	assert.NotContains(t, plainArgs, "--dangerously-skip-permissions")
	assert.Contains(t, devArgs, "--dangerously-skip-permissions")
}

func TestProfileArgsExcludingDevFlag_StripsSkipPermissions(t *testing.T) {
	out := profileArgsExcludingDevFlag(config.AgentProfile{
		Args: []string{"--dangerously-skip-permissions", "--append-system-prompt", "hello"},
	})
	assert.Equal(t, []string{"--append-system-prompt", "hello"}, out)
}

func TestResolveTimeout_OverridePrecedence(t *testing.T) {
	profile := config.AgentProfile{TimeoutSeconds: 600}
	job := &executor.ExecutionJob{Metadata: map[string]any{"timeout_seconds_override": 120}}
	got := resolveTimeout(profile, job)
	assert.Equal(t, 120*time.Second, got, "metadata override beats profile")

	job = &executor.ExecutionJob{Metadata: map[string]any{"timeout_seconds_override": 30}}
	got = resolveTimeout(profile, job)
	assert.Equal(t, 600*time.Second, got, "out-of-range override falls through to profile")
}
