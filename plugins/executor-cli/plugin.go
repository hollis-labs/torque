package executorcli

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

	"github.com/hollis-labs/clockwork-manifold/internal/agentfile"
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// cancelGraceFromEnv parses CLOCKWORK_SCHED_CANCEL_GRACE into a duration for
// SIGTERM→SIGKILL child-process grace. Shares the scheduler's env var so one
// knob controls both layers; falls back to 5s (CW-20260418-0005 default).
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

// stderrTailBytes caps how much stderr we surface in result.Reason on
// failure. The full stream is still teed to the sidecar file — this is
// just the inline tail callers (scheduler, MCP clients) see.
const stderrTailBytes = 8 * 1024

// stdoutTailBytes caps how much stdout we surface in result.Reason on
// failure when stderr is empty (common with claude --print, which writes
// everything to stdout). Smaller than stderrTailBytes because stdout is
// prose-heavy and the tail is a fallback diagnostic, not the primary
// signal. See Bug CW-20260417-0031.
const stdoutTailBytes = 2 * 1024

// Compile-time check that CLIExecutor satisfies the Executor interface.
var _ executor.Executor = (*CLIExecutor)(nil)

// CLIExecutor is an executor plugin that spawns a CLI process and parses
// CLOCKWORK_* signals from its stdout.
type CLIExecutor struct {
	profiles config.ProfileMap

	mu      sync.Mutex
	current *exec.Cmd
}

// New creates a new CLIExecutor with the given agent profiles.
func New(profiles config.ProfileMap) *CLIExecutor {
	return &CLIExecutor{profiles: profiles}
}

// Name returns the executor's registered name.
func (e *CLIExecutor) Name() string {
	return "cli"
}

// Capabilities reports what this executor supports.
func (e *CLIExecutor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{
		SupportsStreaming:   true,
		SupportsTools:       false,
		SupportsSandbox:     true,
		SupportsPermissions: false,
	}
}

// Validate checks whether a job is valid for this executor before dispatch.
// A missing-or-unloaded agent profile is a config-permanent failure —
// retrying can't recover it — so it's surfaced as executor.PermanentError.
// The scheduler treats PermanentError as block-no-retry (CW-20260418-0010).
// TaskID-shape errors are non-permanent because nothing in this executor
// produced them; the caller owns the taxonomy for those.
func (e *CLIExecutor) Validate(job *executor.ExecutionJob) error {
	if err := job.Validate(); err != nil {
		return err
	}
	profile := resolveProfile(e.profiles, job)
	if profile.Command == "" && profile.Provider == "" {
		return executor.NewPermanentError(
			fmt.Errorf("profile %q has neither command nor provider set", job.AgentProfile),
		)
	}
	return nil
}

// Run executes the job by spawning the resolved CLI process and parsing its stdout.
// Streaming events are dispatched through cb. Returns the final ExecutionResult.
func (e *CLIExecutor) Run(ctx context.Context, job *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	profile := resolveProfile(e.profiles, job)

	// Agent file v1 (CW-20260417-0082): load the referenced spec at dispatch
	// time. Parse failures block the run with a clear reason rather than
	// failing the mutation that created the task — the file may have been
	// valid when the task was created and edited later.
	agent, err := loadAgentFile(job)
	if err != nil {
		return &executor.ExecutionResult{Status: "blocked", Reason: err.Error()}, nil
	}
	if agent != nil && len(agent.Tools) > 0 {
		log.Printf("executor-cli: agent file declares tools=%v (advisory only in v1; no enforcement) task=%s", agent.Tools, job.TaskID)
	}

	spec, err := buildCommandSpec(profile, job, agent)
	if err != nil {
		return nil, fmt.Errorf("build command spec: %w", err)
	}

	// Determine timeout: task metadata override wins, then profile, then job limits.
	timeout := resolveTimeout(profile, job)

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	//nolint:gosec // command comes from resolved profile config
	cmd := exec.CommandContext(runCtx, spec.Command, spec.Args...)

	// Graceful cancellation for the child process (CW-20260418-0005).
	// Default behavior of exec.CommandContext is to send SIGKILL on ctx
	// cancel; override Cancel so we send SIGTERM first, then rely on
	// WaitDelay to escalate to SIGKILL if the child doesn't exit in time.
	// Grace is sourced from CLOCKWORK_SCHED_CANCEL_GRACE (default 5s) —
	// same env the scheduler reads, so operators have one knob.
	grace := cancelGraceFromEnv()
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = grace

	if job.WorkingDir != "" {
		cmd.Dir = job.WorkingDir
	}

	// Build env: start from OS env, filter secrets, inject task vars.
	baseEnv := os.Environ()
	opts := executor.FilterEnvOpts{
		StripPrefixes: profile.EnvStripPrefixes,
		ExtraVars: []string{
			"CLOCKWORK_TASK_ID=" + job.TaskID,
			fmt.Sprintf("CLOCKWORK_RUN_ID=%d", job.RunID),
		},
	}
	// Environment precedence (CW-20260417-0082): agent_file.environment is
	// applied first so task-level overrides win when the same key is set in
	// both. Secrets are always filtered regardless of source.
	if agent != nil {
		for k, v := range agent.Environment {
			if _, overridden := job.Environment[k]; overridden {
				continue
			}
			if !executor.LooksLikeSecret(k) {
				opts.ExtraVars = append(opts.ExtraVars, k+"="+v)
			}
		}
	}
	// Inject any job-level environment overrides as extra vars, skipping secrets.
	for k, v := range job.Environment {
		if !executor.LooksLikeSecret(k) {
			opts.ExtraVars = append(opts.ExtraVars, k+"="+v)
		}
	}
	filtered, _ := executor.FilterEnv(baseEnv, opts)
	cmd.Env = filtered

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

	if spec.UseStreamJSON {
		err = e.runStreamJSON(runCtx, cmd, job, cb, result)
	} else {
		err = e.runPrintMode(runCtx, cmd, job, cb, result)
	}

	result.Duration = time.Since(start)

	// Timeout: context canceled or deadline exceeded.
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

// attachStderrCapture configures cmd.Stderr to capture into an in-memory
// buffer AND tee to a sidecar log file at
// $CLOCKWORK_DATA_DIR/runs/<run_id>.stderr.log (falling back to os.TempDir
// when the env var is empty). The sidecar path is created on demand; if
// creation fails we log a warning and continue with buffer-only capture —
// losing stderr entirely (the old behavior) is never acceptable.
//
// Returns the stderr buffer (always non-nil) and a close function that
// must be called after cmd.Wait() to close the sidecar file.
func attachStderrCapture(cmd *exec.Cmd, runID int64) (*bytes.Buffer, func()) {
	buf := &bytes.Buffer{}
	closer := func() {}

	dataDir := os.Getenv("CLOCKWORK_DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "clockwork")
	}
	runsDir := filepath.Join(dataDir, "runs")

	// Best-effort: if MkdirAll fails (permissions, ro fs, etc.) we still
	// capture to the buffer. Log so operators can see the degradation.
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		log.Printf("executor-cli: stderr sidecar dir unavailable (%s): %v — using buffer only", runsDir, err)
		cmd.Stderr = buf
		return buf, closer
	}

	sidecarPath := filepath.Join(runsDir, fmt.Sprintf("%d.stderr.log", runID))
	f, err := os.OpenFile(sidecarPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("executor-cli: stderr sidecar open failed (%s): %v — using buffer only", sidecarPath, err)
		cmd.Stderr = buf
		return buf, closer
	}

	cmd.Stderr = io.MultiWriter(buf, f)
	closer = func() {
		_ = f.Close()
	}
	return buf, closer
}

// tailBytes returns the trailing up-to-max bytes of data, trimmed of
// surrounding whitespace. Shared by stderr and stdout tail helpers so
// they never drift.
func tailBytes(data []byte, max int) string {
	if len(data) == 0 {
		return ""
	}
	if len(data) > max {
		data = data[len(data)-max:]
	}
	return strings.TrimSpace(string(data))
}

// failureReason returns the best available diagnostic for a failed run:
// stderr tail if present, otherwise the stdout tail, otherwise fallback.
// Fallback is returned verbatim — callers typically pass waitErr.Error()
// so the run still has *some* signal when both streams are empty.
func failureReason(stderrBuf *bytes.Buffer, stdout string, fallback string) string {
	if stderrBuf != nil {
		if tail := tailBytes(stderrBuf.Bytes(), stderrTailBytes); tail != "" {
			return tail
		}
	}
	if tail := tailBytes([]byte(stdout), stdoutTailBytes); tail != "" {
		return tail
	}
	return fallback
}

// Kill sends SIGKILL to the currently running process, if any.
func (e *CLIExecutor) Kill() {
	e.mu.Lock()
	cmd := e.current
	e.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// runPrintMode spawns the command and parses stdout line-by-line for
// CLOCKWORK_* signals.
//
// Stderr is captured into a buffer AND teed to a sidecar log file (see
// attachStderrCapture) so failures can be diagnosed after the fact
// (Bug CW-20260417-0024). On genuine failure (exit != 0, no terminal
// signal, no stdout signal rescue) the trailing stderr lands in
// result.Reason so the scheduler can persist it as the run's
// ErrorMessage.
//
// Exit-code-tolerant completion: exit code 1 accompanied by a valid
// CLOCKWORK_* terminal signal is treated as success. claude CLI has
// been observed exiting 1 after fully successful runs
// (Bug CW-20260417-0025).
func (e *CLIExecutor) runPrintMode(_ context.Context, cmd *exec.Cmd, job *executor.ExecutionJob, cb executor.EventCallback, result *executor.ExecutionResult) error {
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
	// Accumulate all stdout lines for a fallback re-scan in case no terminal
	// signal was matched on the first pass (e.g. the agent printed the
	// signal mid-paragraph; FE's executor does the same rescue pass).
	var accumulated strings.Builder

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		accumulated.WriteString(line)
		accumulated.WriteByte('\n')
		if e.dispatchLineSignal(line, result, cb) {
			gotTerminal = true
		}
	}

	waitErr := cmd.Wait()

	// Fallback rescue: if no terminal signal arrived on the per-line pass,
	// re-scan accumulated stdout. ParseLine is line-oriented, so this only
	// helps if the signal landed on its own line but was somehow missed
	// (redundant safety; cheap).
	if !gotTerminal {
		if e.fallbackTextParse(accumulated.String(), result, cb) {
			gotTerminal = true
		}
	}

	// Exit-code-tolerant success path: a valid terminal signal wins over
	// a nonzero exit. The stderr/stdout is still logged for observability.
	if gotTerminal {
		if waitErr != nil {
			log.Printf("executor-cli: %s exited with %v after terminal signal — treating as success-with-noisy-exit (task=%s run=%d)",
				cmd.Path, waitErr, job.TaskID, job.RunID)
		}
		return nil
	}

	// No terminal signal. Exit status determines failure.
	if waitErr != nil {
		result.Status = "failed"
		result.Reason = failureReason(stderrBuf, accumulated.String(), waitErr.Error())
		log.Printf("executor-cli: print-mode failure (task=%s run=%d exit=%v): %s",
			job.TaskID, job.RunID, waitErr, result.Reason)
		return nil
	}

	// Process exited 0 but never signaled completion — treat as failure
	// with an explicit reason. Prefer stdout tail (the agent may have
	// emitted a human-readable diagnostic) before the generic fallback.
	if result.Status == "" {
		result.Status = "failed"
		result.Reason = failureReason(nil, accumulated.String(),
			"process exited without emitting a CLOCKWORK_* terminal signal")
		log.Printf("executor-cli: print-mode failure (task=%s run=%d exit=0 no-signal): %s",
			job.TaskID, job.RunID, result.Reason)
	}

	return nil
}

// dispatchLineSignal parses a single stdout line and updates result / emits
// callback events. Returns true when a terminal signal (done/review/blocked)
// was dispatched.
func (e *CLIExecutor) dispatchLineSignal(line string, result *executor.ExecutionResult, cb executor.EventCallback) bool {
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
		handleArtifactSignal(sig, result, cb, line)

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

// fallbackTextParse re-scans accumulated stdout for terminal signals when
// the primary per-line pass missed them. Mirrors FE executor.go:443 —
// defensive recovery for output the agent didn't format cleanly.
// Returns true if a terminal signal was found.
func (e *CLIExecutor) fallbackTextParse(text string, result *executor.ExecutionResult, cb executor.EventCallback) bool {
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

// runStreamJSON spawns the command and parses stdout as NDJSON produced
// by `claude --verbose --output-format stream-json --json-schema <schema>`.
//
// The primary completion signal is the structured `result` event
// (executor.AgentResult) — claude emits this because of --json-schema,
// which makes the outcome unambiguous regardless of what the agent said
// in freeform markdown. If the result event is missing (network hiccup,
// flag mismatch, etc.) we fall back to rescanning accumulated stdout for
// line-formatted CLOCKWORK_* signals — FE's behavior in executor.go:443.
//
// Stderr is captured and teed to a sidecar log; on genuine failure the
// tail of stderr lands in result.Reason. Exit code 1 after a valid
// terminal signal is treated as success-with-noisy-exit
// (Bug CW-20260417-0025).
func (e *CLIExecutor) runStreamJSON(_ context.Context, cmd *exec.Cmd, job *executor.ExecutionJob, cb executor.EventCallback, result *executor.ExecutionResult) error {
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
	// Accumulate content for the fallback rescue parser.
	var accumulated strings.Builder

	parseErr := executor.ParseStreamJSON(stdout, func(ev executor.StreamEvent) {
		switch ev.Type {
		case executor.StreamEventLogLine:
			accumulated.WriteString(ev.Text)
			accumulated.WriteByte('\n')
			if cb != nil {
				cb(executor.LogEvent(ev.Text))
			}

		case executor.StreamEventSignal:
			sig := ev.Signal
			accumulated.WriteString(sig.Payload)
			accumulated.WriteByte('\n')
			switch sig.Type {
			case executor.SignalDone:
				result.Status = "done"
				gotTerminal = true
				if cb != nil {
					cb(executor.SignalEvent("CLOCKWORK_DONE", ""))
				}
			case executor.SignalReview:
				result.Status = "review"
				gotTerminal = true
				if cb != nil {
					cb(executor.SignalEvent("CLOCKWORK_REVIEW", ""))
				}
			case executor.SignalBlocked:
				result.Status = "blocked"
				result.Reason = sig.Payload
				gotTerminal = true
				if cb != nil {
					cb(executor.SignalEvent("CLOCKWORK_BLOCKED", sig.Payload))
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
				handleArtifactSignal(sig, result, cb, sig.Payload)
			default:
				if cb != nil {
					cb(executor.SignalEvent(sig.Type.String(), sig.Payload))
				}
			}

		case executor.StreamEventToolUse:
			if ev.ToolUse != nil && cb != nil {
				cb(executor.ToolUseEvent(ev.ToolUse.Name, ev.ToolUse.ArgsSummary))
			}

		case executor.StreamEventResult:
			if ev.Result != nil {
				switch ev.Result.Status {
				case "done":
					result.Status = "done"
					gotTerminal = true
					if cb != nil {
						cb(executor.SignalEvent("CLOCKWORK_DONE", ""))
					}
				case "review":
					result.Status = "review"
					gotTerminal = true
					if cb != nil {
						cb(executor.SignalEvent("CLOCKWORK_REVIEW", ""))
					}
				case "blocked", "error":
					result.Status = "blocked"
					reason := ev.Result.BlockedReason
					if reason == "" {
						reason = ev.Result.Summary
					}
					result.Reason = reason
					gotTerminal = true
					if cb != nil {
						cb(executor.SignalEvent("CLOCKWORK_BLOCKED", reason))
					}
				}
				// Attach produced file list as log-level notes so downstream
				// observers can see what the agent claims to have touched.
				if len(ev.Result.FilesChanged) > 0 && cb != nil {
					cb(executor.LogEvent("files_changed: " + strings.Join(ev.Result.FilesChanged, ", ")))
				}
				// CW-20260417-0036: schema-mode suppresses freeform CLOCKWORK_ARTIFACT
				// signals, so files_changed is the only artifact evidence the agent
				// emits. Auto-classify each path and dispatch as an Artifact event
				// so the scheduler persists records for the deliverable-check gate.
				autoEmitFilesChangedArtifacts(ev.Result.FilesChanged, result, cb)
				if ev.Result.Summary != "" && cb != nil {
					cb(executor.SignalEvent("CLOCKWORK_NOTE", ev.Result.Summary))
				}
			}
			if ev.Tokens != nil {
				p := int(ev.Tokens.InputTokens)
				c := int(ev.Tokens.OutputTokens)
				cost := ev.Tokens.CostUSD
				result.Tokens.PromptTokens += p
				result.Tokens.CompletionTokens += c
				result.Tokens.Cost += cost
				result.Cost += cost
				if cb != nil {
					cb(executor.TokenEvent(p, c, cost))
				}
			}
		}
	})
	if parseErr != nil {
		log.Printf("executor-cli: stream-json parse aborted (task=%s run=%d): %v", job.TaskID, job.RunID, parseErr)
	}

	waitErr := cmd.Wait()

	// Fallback: if we never got a structured result OR a mid-stream terminal
	// signal, rescan accumulated content for CLOCKWORK_* signals. Matches
	// FE's fallbackTextParse.
	if !gotTerminal {
		if e.fallbackTextParse(accumulated.String(), result, cb) {
			gotTerminal = true
		}
	}

	if gotTerminal {
		if waitErr != nil {
			log.Printf("executor-cli: %s exited with %v after terminal signal — treating as success-with-noisy-exit (task=%s run=%d)",
				cmd.Path, waitErr, job.TaskID, job.RunID)
		}
		return nil
	}

	// Genuine failure: no terminal signal arrived.
	if waitErr != nil {
		if result.Status == "" {
			result.Status = "failed"
		}
		result.Reason = failureReason(stderrBuf, accumulated.String(), waitErr.Error())
		log.Printf("executor-cli: stream-json failure (task=%s run=%d exit=%v): %s",
			job.TaskID, job.RunID, waitErr, result.Reason)
		return nil
	}

	if result.Status == "" {
		result.Status = "failed"
		result.Reason = failureReason(nil, accumulated.String(),
			"stream-json ended without a result event or CLOCKWORK_* signal")
		log.Printf("executor-cli: stream-json failure (task=%s run=%d exit=0 no-result): %s",
			job.TaskID, job.RunID, result.Reason)
	}
	return nil
}

// screenshotExtensions lists file extensions that should be classified as
// the "screenshot" artifact type. Anything else falls back to "diff".
// Extension matching is case-insensitive.
var screenshotExtensions = map[string]struct{}{
	".png":  {},
	".jpg":  {},
	".jpeg": {},
	".gif":  {},
	".webp": {},
}

// classifyArtifactType maps a file path to an Artifact.Type label by file
// extension. Image extensions become "screenshot"; everything else becomes
// "diff" — which matches the deliverable presets the gate enforces.
func classifyArtifactType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := screenshotExtensions[ext]; ok {
		return "screenshot"
	}
	return "diff"
}

// autoEmitFilesChangedArtifacts converts each path in files_changed into an
// Artifact tagged with metadata.origin=agent-auto, appends it to result and
// dispatches an ArtifactEvent so the scheduler persists it. Skips any path
// already present in result.Artifacts (manual CLOCKWORK_ARTIFACT signals win
// — operators may have set richer Type/Content values).
//
// See CW-20260417-0036: claude --json-schema suppresses freeform artifact
// signals, so files_changed is the only evidence the deliverable-check gate
// has to work with.
func autoEmitFilesChangedArtifacts(paths []string, result *executor.ExecutionResult, cb executor.EventCallback) {
	if len(paths) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(result.Artifacts))
	for _, a := range result.Artifacts {
		if a.FilePath != "" {
			seen[a.FilePath] = struct{}{}
		}
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		art := executor.Artifact{
			Type:     classifyArtifactType(p),
			FilePath: p,
			Metadata: map[string]interface{}{"origin": "agent-auto"},
		}
		result.Artifacts = append(result.Artifacts, art)
		if cb != nil {
			cb(executor.ArtifactEvent(art))
		}
	}
}

// handleArtifactSignal parses a CLOCKWORK_ARTIFACT signal payload and either
// appends the resulting Artifact to result.Artifacts and emits an ArtifactEvent,
// or — if the payload is malformed — emits a LogEvent containing fallbackLine
// so the raw line remains visible. Never panics, never fails the run.
func handleArtifactSignal(
	sig executor.ParsedSignal,
	result *executor.ExecutionResult,
	cb executor.EventCallback,
	fallbackLine string,
) {
	art, err := executor.ParseArtifactPayload(sig.Payload)
	if err != nil {
		if cb != nil {
			cb(executor.LogEvent(fallbackLine))
		}
		return
	}
	result.Artifacts = append(result.Artifacts, art)
	if cb != nil {
		cb(executor.ArtifactEvent(art))
	}
}

// loadAgentFile resolves and loads the task's agent file (if any) at dispatch.
// Returns nil, nil when the task has no agent_file set. Returns a descriptive
// error when the path can't be resolved or parsed — the executor converts
// this into a blocked run so operators can fix the file without losing the
// task. Contents are read here (not at task mutation time) to tolerate files
// that existed at create but were deleted/edited later.
func loadAgentFile(job *executor.ExecutionJob) (*agentfile.AgentFile, error) {
	if job.AgentFile == "" {
		return nil, nil
	}
	resolved, err := agentfile.Resolve(job.AgentFile, job.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("agent_file: %w", err)
	}
	return agentfile.Load(resolved)
}
