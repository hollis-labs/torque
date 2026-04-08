package executorcli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

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
func (e *CLIExecutor) Validate(job *executor.ExecutionJob) error {
	if err := job.Validate(); err != nil {
		return err
	}
	profile := resolveProfile(e.profiles, job)
	if profile.Command == "" && profile.Provider == "" {
		return fmt.Errorf("profile %q has neither command nor provider set", job.AgentProfile)
	}
	return nil
}

// Run executes the job by spawning the resolved CLI process and parsing its stdout.
// Streaming events are dispatched through cb. Returns the final ExecutionResult.
func (e *CLIExecutor) Run(ctx context.Context, job *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	profile := resolveProfile(e.profiles, job)

	spec, err := buildCommandSpec(profile, job)
	if err != nil {
		return nil, fmt.Errorf("build command spec: %w", err)
	}

	// Determine timeout: profile wins, then job limits fallback.
	var timeout time.Duration
	if profile.TimeoutSeconds > 0 {
		timeout = time.Duration(profile.TimeoutSeconds) * time.Second
	} else {
		timeout = job.Limits.EffectiveTimeout()
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	//nolint:gosec // command comes from resolved profile config
	cmd := exec.CommandContext(runCtx, spec.Command, spec.Args...)

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

// Kill sends SIGKILL to the currently running process, if any.
func (e *CLIExecutor) Kill() {
	e.mu.Lock()
	cmd := e.current
	e.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// runPrintMode spawns the command and parses stdout line-by-line for CLOCKWORK_* signals.
func (e *CLIExecutor) runPrintMode(_ context.Context, cmd *exec.Cmd, job *executor.ExecutionJob, cb executor.EventCallback, result *executor.ExecutionResult) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = nil // discard stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	var gotTerminal bool

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		sig := executor.ParseLine(line)

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

		case executor.SignalCheckpoint, executor.SignalProgress, executor.SignalSubtask, executor.SignalArtifact:
			if cb != nil {
				cb(executor.SignalEvent(sig.Type.String(), sig.Payload))
			}

		default:
			if cb != nil {
				cb(executor.LogEvent(line))
			}
		}
	}

	waitErr := cmd.Wait()
	if waitErr != nil && !gotTerminal {
		result.Status = "failed"
		result.Reason = waitErr.Error()
		return nil
	}

	if !gotTerminal && result.Status == "" {
		result.Status = "failed"
	}

	return nil
}

// runStreamJSON spawns the command and parses stdout as NDJSON.
func (e *CLIExecutor) runStreamJSON(_ context.Context, cmd *exec.Cmd, _ *executor.ExecutionJob, cb executor.EventCallback, result *executor.ExecutionResult) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	executor.ParseStreamJSON(stdout, func(ev executor.StreamEvent) {
		switch ev.Type {
		case executor.StreamEventLogLine:
			if cb != nil {
				cb(executor.LogEvent(ev.Text))
			}

		case executor.StreamEventSignal:
			sig := ev.Signal
			switch sig.Type {
			case executor.SignalDone:
				result.Status = "done"
				if cb != nil {
					cb(executor.SignalEvent("CLOCKWORK_DONE", ""))
				}
			case executor.SignalReview:
				result.Status = "review"
				if cb != nil {
					cb(executor.SignalEvent("CLOCKWORK_REVIEW", ""))
				}
			case executor.SignalBlocked:
				result.Status = "blocked"
				result.Reason = sig.Payload
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
			default:
				if cb != nil {
					cb(executor.SignalEvent(sig.Type.String(), sig.Payload))
				}
			}

		case executor.StreamEventResult:
			if ev.Result != nil {
				switch ev.Result.Status {
				case "done":
					result.Status = "done"
					if cb != nil {
						cb(executor.SignalEvent("CLOCKWORK_DONE", ""))
					}
				case "review":
					result.Status = "review"
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
					if cb != nil {
						cb(executor.SignalEvent("CLOCKWORK_BLOCKED", reason))
					}
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

	waitErr := cmd.Wait()
	if waitErr != nil && result.Status == "" {
		result.Status = "failed"
		result.Reason = waitErr.Error()
	}

	return nil
}
