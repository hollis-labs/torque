// Package executoropencode implements a Clockwork executor that dispatches
// tasks to opencode agent sessions via `opencode run`.
//
// Command shape:
//
//	opencode run \
//	  --agent <task.agent_profile> \
//	  [--model  <task.model>]      \
//	  [--dir    <task.working_dir>] \
//	  "<task.system_prompt>\n\n<task.description>"
//
// Field mapping from the ExecutionJob:
//   - AgentProfile → --agent
//   - Limits (model via metadata["model"]) → --model  (optional)
//   - WorkingDir    → --dir                            (optional)
//   - SystemPrompt + Description → positional message  (required)
//
// Output parsing is identical to the CLI executor's print-mode path:
// stdout is scanned line-by-line for CLOCKWORK_* signals; stderr is
// captured and teed to a sidecar log for post-mortem diagnosis.
package executoropencode

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// cancelGraceFromEnv reads CLOCKWORK_SCHED_CANCEL_GRACE for SIGTERM→SIGKILL
// grace period. Mirrors the CLI executor so one knob controls both layers.
func cancelGraceFromEnv() time.Duration {
	const def = 5 * time.Second
	v := os.Getenv("CLOCKWORK_SCHED_CANCEL_GRACE")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// stderrTailBytes caps the stderr tail surfaced in result.Reason on failure.
const stderrTailBytes = 8 * 1024

// Compile-time check that OpenCodeExecutor satisfies the Executor interface.
var _ executor.Executor = (*OpenCodeExecutor)(nil)

// OpenCodeExecutor dispatches tasks to opencode agent sessions via
// `opencode run`. It requires no agent profile registry — the AgentProfile
// field on the job maps directly to opencode's --agent flag.
type OpenCodeExecutor struct {
	mu      sync.Mutex
	current *exec.Cmd
}

// New creates a new OpenCodeExecutor.
func New() *OpenCodeExecutor {
	return &OpenCodeExecutor{}
}

// Name returns the executor's registered name.
func (e *OpenCodeExecutor) Name() string {
	return "opencode"
}

// Capabilities reports what this executor supports.
func (e *OpenCodeExecutor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{
		SupportsStreaming:   true,
		SupportsTools:       false,
		SupportsSandbox:     false,
		SupportsPermissions: false,
	}
}

// Validate checks whether a job is valid for this executor before dispatch.
// A missing AgentProfile is a config-permanent failure — retrying can't
// recover it. An unresolvable working_dir is also permanent.
func (e *OpenCodeExecutor) Validate(job *executor.ExecutionJob) error {
	if err := job.Validate(); err != nil {
		return err
	}
	if job.AgentProfile == "" {
		return executor.NewPermanentError(
			fmt.Errorf("opencode executor requires agent_profile to be set"),
		)
	}
	if _, err := executor.ResolveWorkingDir(job.WorkingDir); err != nil {
		return executor.NewPermanentError(
			fmt.Errorf("resolve working_dir: %w", err),
		)
	}
	return nil
}

// Run executes the job by spawning `opencode run` and parsing its stdout for
// CLOCKWORK_* signals. Streaming events are dispatched through cb.
func (e *OpenCodeExecutor) Run(ctx context.Context, job *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	args := buildArgs(job)

	timeout := resolveTimeout(job)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	//nolint:gosec // binary is the fixed "opencode" name; args are resolved from job fields
	cmd := exec.CommandContext(runCtx, "opencode", args...)

	// Graceful cancellation: SIGTERM first, then SIGKILL after grace period.
	grace := cancelGraceFromEnv()
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = grace

	// Resolve and apply working_dir (same tilde+$VAR expansion as cli executor).
	resolvedWD, wdErr := executor.ResolveWorkingDir(job.WorkingDir)
	if wdErr != nil {
		return &executor.ExecutionResult{
				Status: "blocked",
				Reason: fmt.Sprintf("resolve working_dir: %v", wdErr),
			}, executor.NewPermanentError(
				fmt.Errorf("resolve working_dir: %w", wdErr),
			)
	}
	if resolvedWD != "" {
		cmd.Dir = resolvedWD
		if resolvedWD != job.WorkingDir {
			log.Printf("executor-opencode: resolved working_dir %q → %q (task=%s run=%d)",
				job.WorkingDir, resolvedWD, job.TaskID, job.RunID)
		}
	}

	// Inject CLOCKWORK_TASK_ID and CLOCKWORK_RUN_ID into the subprocess env.
	cmd.Env = buildEnv(job)

	e.mu.Lock()
	e.current = cmd
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.current = nil
		e.mu.Unlock()
	}()

	start := time.Now()
	result := &executor.ExecutionResult{}
	err := e.runPrintMode(runCtx, cmd, job, cb, result)
	result.Duration = time.Since(start)

	if runCtx.Err() != nil {
		result.Status = "failed"
		result.Reason = "execution timeout"
		return result, nil
	}

	if err != nil {
		return result, err
	}
	return result, nil
}

// buildArgs constructs the opencode run argument list from the job.
//
// Synopsis:
//
//	opencode run --agent <profile> [--model <model>] [--dir <dir>] <message>
//
// The positional message is: system_prompt\n\ndescription (or just description
// when system_prompt is empty). opencode does not have a dedicated
// --system-prompt flag, so we prepend it inline — matching the cli executor's
// prependSystemPrompt helper for providers that lack the flag.
func buildArgs(job *executor.ExecutionJob) []string {
	var args []string
	args = append(args, "run")

	// --agent (required — Validate guards this)
	args = append(args, "--agent", job.AgentProfile)

	// --model from metadata["model"] (optional)
	if model := modelFromMetadata(job.Metadata); model != "" {
		args = append(args, "--model", model)
	}

	// --dir (optional — resolved separately before cmd.Dir is set, but also
	// passed as --dir so opencode itself is oriented to the right root when
	// it initialises its own session).
	resolvedWD, err := executor.ResolveWorkingDir(job.WorkingDir)
	if err == nil && resolvedWD != "" {
		args = append(args, "--dir", resolvedWD)
	}

	// positional message: system_prompt + description
	args = append(args, buildMessage(job))

	return args
}

// buildMessage composes the positional message argument. When SystemPrompt is
// set it is prepended as "System: <prompt>\n\n<description>" so the agent
// sees the per-task preamble before the task body. Mirrors prependSystemPrompt
// in the cli executor's adapters.go.
func buildMessage(job *executor.ExecutionJob) string {
	if job.SystemPrompt == "" {
		return job.Description
	}
	return "System: " + job.SystemPrompt + "\n\n" + job.Description
}

// modelFromMetadata extracts an optional model string from job metadata under
// the key "model". Returns "" when not set or not a string.
func modelFromMetadata(md map[string]any) string {
	if md == nil {
		return ""
	}
	v, ok := md["model"]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return ""
}

// resolveTimeout picks the effective run timeout in priority order:
//  1. job.Metadata["timeout_seconds_override"] within [60, 7200]
//  2. job.Limits.EffectiveTimeout() fallback (default 5 min)
//
// opencode sessions can be long (they drive full agent runs), so the default
// 5-minute limit from ExecutionLimits should be overridden via task metadata
// when the orchestrator knows the run will take longer.
func resolveTimeout(job *executor.ExecutionJob) time.Duration {
	if job != nil {
		if secs, ok := taskTimeoutOverride(job.Metadata); ok {
			return time.Duration(secs) * time.Second
		}
	}
	if job == nil {
		return executor.ExecutionLimits{}.EffectiveTimeout()
	}
	return job.Limits.EffectiveTimeout()
}

// taskTimeoutOverride mirrors the cli executor's helper — extracts
// timeout_seconds_override from metadata, valid range [60, 7200].
func taskTimeoutOverride(md map[string]any) (int, bool) {
	if md == nil {
		return 0, false
	}
	raw, ok := md["timeout_seconds_override"]
	if !ok {
		return 0, false
	}
	var secs int
	switch v := raw.(type) {
	case int:
		secs = v
	case int64:
		secs = int(v)
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		secs = int(v)
	default:
		return 0, false
	}
	const (
		minSecs = 60
		maxSecs = 7200
	)
	if secs < minSecs || secs > maxSecs {
		return 0, false
	}
	return secs, true
}

// buildEnv constructs the subprocess env: OS env stripped of sensitive vars,
// with CLOCKWORK_TASK_ID and CLOCKWORK_RUN_ID injected. Job-level environment
// overrides are applied last, skipping keys that look like secrets.
func buildEnv(job *executor.ExecutionJob) []string {
	baseEnv := os.Environ()
	opts := executor.FilterEnvOpts{
		ExtraVars: []string{
			"CLOCKWORK_TASK_ID=" + job.TaskID,
			fmt.Sprintf("CLOCKWORK_RUN_ID=%d", job.RunID),
		},
	}
	for k, v := range job.Environment {
		if !executor.LooksLikeSecret(k) {
			opts.ExtraVars = append(opts.ExtraVars, k+"="+v)
		}
	}
	filtered, _ := executor.FilterEnv(baseEnv, opts)
	return filtered
}

// attachStderrCapture configures cmd.Stderr to capture into an in-memory
// buffer AND tee to a sidecar log file at
// $CLOCKWORK_DATA_DIR/runs/<run_id>.stderr.log. Mirrors the cli executor's
// implementation so diagnostic output is preserved on failure.
func attachStderrCapture(cmd *exec.Cmd, runID int64) (*bytes.Buffer, func()) {
	buf := &bytes.Buffer{}
	closer := func() {}

	dataDir := os.Getenv("CLOCKWORK_DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "clockwork")
	}
	runsDir := filepath.Join(dataDir, "runs")

	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		log.Printf("executor-opencode: stderr sidecar dir unavailable (%s): %v — using buffer only", runsDir, err)
		cmd.Stderr = buf
		return buf, closer
	}

	sidecarPath := filepath.Join(runsDir, fmt.Sprintf("%d.stderr.log", runID))
	f, err := os.OpenFile(sidecarPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("executor-opencode: stderr sidecar open failed (%s): %v — using buffer only", sidecarPath, err)
		cmd.Stderr = buf
		return buf, closer
	}

	cmd.Stderr = io.MultiWriter(buf, f)
	closer = func() {
		_ = f.Close()
	}
	return buf, closer
}

// Kill sends SIGKILL to the currently running process, if any.
func (e *OpenCodeExecutor) Kill() {
	e.mu.Lock()
	cmd := e.current
	e.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// runPrintMode spawns the opencode command and scans stdout line-by-line for
// CLOCKWORK_* signals. Stderr is captured + teed to a sidecar log. Mirrors
// the cli executor's runPrintMode.
func (e *OpenCodeExecutor) runPrintMode(_ context.Context, cmd *exec.Cmd, job *executor.ExecutionJob, cb executor.EventCallback, result *executor.ExecutionResult) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderrBuf, closeStderr := attachStderrCapture(cmd, job.RunID)
	defer closeStderr()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	var gotTerminal bool
	var accumulated strings.Builder

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		accumulated.WriteString(line)
		accumulated.WriteByte('\n')
		if dispatchLineSignal(line, result, cb) {
			gotTerminal = true
		}
	}

	waitErr := cmd.Wait()

	// Fallback rescue: rescan accumulated stdout for terminal signals.
	if !gotTerminal {
		if fallbackTextParse(accumulated.String(), result, cb) {
			gotTerminal = true
		}
	}

	// Exit-code-tolerant: a terminal signal wins over a nonzero exit.
	if gotTerminal {
		if waitErr != nil {
			log.Printf("executor-opencode: opencode exited with %v after terminal signal — treating as success-with-noisy-exit (task=%s run=%d)",
				waitErr, job.TaskID, job.RunID)
		}
		return nil
	}

	if waitErr != nil {
		result.Status = "failed"
		result.Reason = failureReason(stderrBuf, accumulated.String(), waitErr.Error())
		log.Printf("executor-opencode: failure (task=%s run=%d exit=%v): %s",
			job.TaskID, job.RunID, waitErr, result.Reason)
		return nil
	}

	// Exited 0 but never signaled completion.
	if result.Status == "" {
		result.Status = "failed"
		result.Reason = failureReason(nil, accumulated.String(),
			"process exited without emitting a CLOCKWORK_* terminal signal")
		log.Printf("executor-opencode: failure (task=%s run=%d exit=0 no-signal): %s",
			job.TaskID, job.RunID, result.Reason)
	}
	return nil
}

// dispatchLineSignal parses a single stdout line for CLOCKWORK_* signals,
// updates result, and emits callback events. Returns true on a terminal signal.
func dispatchLineSignal(line string, result *executor.ExecutionResult, cb executor.EventCallback) bool {
	sig := executor.ParseLine(line)
	switch sig.Type {
	case executor.SignalDone:
		result.Status = "done"
		if cb != nil {
			cb(executor.SignalEvent("CLOCKWORK_DONE", ""))
		}
		return true

	case executor.SignalReview:
		result.Status = "review"
		if cb != nil {
			cb(executor.SignalEvent("CLOCKWORK_REVIEW", ""))
		}
		return true

	case executor.SignalBlocked:
		result.Status = "blocked"
		result.Reason = sig.Payload
		if cb != nil {
			cb(executor.SignalEvent("CLOCKWORK_BLOCKED", sig.Payload))
		}
		return true

	case executor.SignalNote:
		if cb != nil {
			cb(executor.SignalEvent("CLOCKWORK_NOTE", sig.Payload))
		}

	case executor.SignalNewTask:
		if cb != nil {
			cb(executor.SignalEvent("CLOCKWORK_TASK", sig.Payload))
		}

	case executor.SignalTokens:
		p, c, cost := executor.ParseTokenPayload(sig.Payload)
		result.Tokens.PromptTokens += int(p)
		result.Tokens.CompletionTokens += int(c)
		result.Tokens.Cost += cost
		result.Cost += cost
		if cb != nil {
			cb(executor.TokenEvent(int(p), int(c), cost))
		}

	case executor.SignalArtifact:
		art, err := executor.ParseArtifactPayload(sig.Payload)
		if err != nil {
			if cb != nil {
				cb(executor.LogEvent(line))
			}
		} else {
			result.Artifacts = append(result.Artifacts, art)
			if cb != nil {
				cb(executor.ArtifactEvent(art))
			}
		}

	case executor.SignalCheckpoint, executor.SignalProgress, executor.SignalSubtask, executor.SignalSubtodoDone:
		if cb != nil {
			cb(executor.SignalEvent(sig.Type.String(), sig.Payload))
		}

	default:
		if cb != nil {
			cb(executor.LogEvent(line))
		}
	}
	return false
}

// fallbackTextParse rescans accumulated stdout for terminal signals when the
// per-line pass missed them. Mirrors cli executor's fallbackTextParse.
func fallbackTextParse(text string, result *executor.ExecutionResult, cb executor.EventCallback) bool {
	var found bool
	for _, line := range strings.Split(text, "\n") {
		sig := executor.ParseLine(line)
		switch sig.Type {
		case executor.SignalDone:
			if result.Status == "" {
				result.Status = "done"
				if cb != nil {
					cb(executor.SignalEvent("CLOCKWORK_DONE", ""))
				}
			}
			found = true
		case executor.SignalReview:
			if result.Status == "" {
				result.Status = "review"
				if cb != nil {
					cb(executor.SignalEvent("CLOCKWORK_REVIEW", ""))
				}
			}
			found = true
		case executor.SignalBlocked:
			if result.Status == "" {
				result.Status = "blocked"
				result.Reason = sig.Payload
				if cb != nil {
					cb(executor.SignalEvent("CLOCKWORK_BLOCKED", sig.Payload))
				}
			}
			found = true
		}
	}
	return found
}

// failureReason returns the best available diagnostic for a failed run.
func failureReason(stderrBuf *bytes.Buffer, stdout string, fallback string) string {
	if stderrBuf != nil {
		if tail := tailBytes(stderrBuf.Bytes(), stderrTailBytes); tail != "" {
			return tail
		}
	}
	const stdoutTail = 2 * 1024
	if tail := tailBytes([]byte(stdout), stdoutTail); tail != "" {
		return tail
	}
	return fallback
}

// tailBytes returns the trailing up-to-max bytes of data, trimmed of whitespace.
func tailBytes(data []byte, max int) string {
	if len(data) == 0 {
		return ""
	}
	if len(data) > max {
		data = data[len(data)-max:]
	}
	return strings.TrimSpace(string(data))
}
