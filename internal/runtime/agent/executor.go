package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/toolbroker"
	"github.com/hollis-labs/go-providers/provider"
)

// Executor adapts agent.Boot(Mode=ModeOneShot) to the executor.Executor
// contract used by the scheduler's worker pool. Registered as "cli" — the
// slot the legacy cliexec.CLIExecutor occupied.
//
// One instance is shared across all CLI tasks; goroutine-safe.
type Executor struct {
	deps *Dependencies
}

// Compile-time check that Executor satisfies the executor.Executor interface.
var _ executor.Executor = (*Executor)(nil)

// NewExecutor constructs an Executor bound to deps. Call after deps.Sessions
// has been populated by NewManager(deps); Run uses Boot which uses Sessions.
func NewExecutor(deps *Dependencies) *Executor {
	return &Executor{deps: deps}
}

// Tools returns the unified tool-broker (selection + permission engine +
// audit log). Nil-safe — when deps.Tools is nil the routes' default broker
// (toolbroker.NewDefault) is what registered tools see.
func (e *Executor) Tools() *toolbroker.ToolRouter { return e.deps.Tools }

// Name returns the executor's registered name. Inherits the "cli" slot the
// legacy cliexec executor occupied so existing profiles/tasks dispatch
// transparently across the migration.
func (e *Executor) Name() string { return "cli" }

// Capabilities reports what this Executor supports. Mirrors the legacy
// cliexec capabilities; SupportsSandbox stays false until Phase F (per-task
// sandbox profile via go-sandbox v0.2.0's AllowLoopback) lands.
func (e *Executor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{
		SupportsStreaming:   true,
		SupportsTools:       true,
		SupportsSandbox:     false,
		SupportsPermissions: false,
	}
}

// Validate rejects jobs the executor cannot run. Mirrors cliexec.Validate.
// Permanent failures (unknown provider, missing profile) wrap as
// executor.PermanentError so the scheduler treats them as block-no-retry.
func (e *Executor) Validate(job *executor.ExecutionJob) error {
	if err := job.Validate(); err != nil {
		return err
	}
	profile := config.GetProfileOrDefault(e.deps.Profiles, job.AgentProfile)
	// Validate is provider-existence only — PTY argument doesn't affect
	// which providers are accepted, so pass false. The actual PTY decision
	// is re-made at Boot time per the Mode + profile + override matrix.
	if _, _, err := adapterFor(profile, job.AgentProfile, false); err != nil {
		return executor.NewPermanentError(err)
	}
	if _, err := executor.ResolveWorkingDir(job.WorkingDir); err != nil {
		return executor.NewPermanentError(fmt.Errorf("resolve working_dir: %w", err))
	}
	return nil
}

// Run executes the job. Internally:
//
//  1. Resolves the working dir (PermanentError on bad shape).
//  2. Builds an Options from the job and calls agent.Boot(Mode=ModeOneShot).
//  3. Boot drives Start + SendInput + Stop synchronously; events flow
//     through eventFanout into the translation goroutine.
//  4. Returns an ExecutionResult shaped per the lifecycle manager's contract
//     (status: done / failed / blocked; reason; tokens).
//
// Cancellation: the supplied ctx is honored by Boot via the inner manager.
// The scheduler's worker pool drives ctx.Done — worker shutdown cancels.
func (e *Executor) Run(ctx context.Context, job *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	resolvedWD, err := executor.ResolveWorkingDir(job.WorkingDir)
	if err != nil {
		return &executor.ExecutionResult{Status: "blocked", Reason: fmt.Sprintf("resolve working_dir: %v", err)},
			executor.NewPermanentError(fmt.Errorf("resolve working_dir: %w", err))
	}
	if resolvedWD == "" {
		return &executor.ExecutionResult{Status: "blocked", Reason: "working_dir is required"},
			executor.NewPermanentError(errors.New("agent.Executor: working_dir is required"))
	}

	// Allocate the per-call event fanout chan + drain goroutine. cliexec used
	// 64 deep; preserve. The drain goroutine accumulates token usage on the
	// result and forwards LogEvent / ToolUseEvent / TokenEvent through cb.
	const fanoutDepth = 64
	fanout := make(chan provider.StreamEvent, fanoutDepth)

	result := &executor.ExecutionResult{}
	var (
		fanoutWG      sync.WaitGroup
		streamErr     error
		streamErrOnce sync.Once
	)
	fanoutWG.Add(1)
	go func() {
		defer fanoutWG.Done()
		for ev := range fanout {
			translateStreamEvent(ev, result, cb, func(s string) {
				streamErrOnce.Do(func() { streamErr = errors.New(s) })
			})
		}
	}()

	// Apply a per-job timeout (matches cliexec.Run's resolveTimeout).
	profile := config.GetProfileOrDefault(e.deps.Profiles, job.AgentProfile)
	opts := optsFromJob(job, resolvedWD).withEventFanout(fanout)
	timeout := resolveTimeout(profile, opts)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Boot drives Start + SendInput + Stop synchronously for ModeOneShot.
	// Returns once the turn has run; sess.ExitCode + sess.Status reflect
	// the outcome.
	sess, bootErr := Boot(runCtx, e.deps, opts)

	// Drain the fanout. Boot's inner Stop closes the inner read loop; the
	// lib-side fanout chan stops receiving once Stop returns. We close the
	// chan from this side; the goroutine sees the close and exits.
	close(fanout)
	fanoutWG.Wait()

	// Token accounting: the drain goroutine populates result.Tokens. Cost
	// stays zero — scheduler.cost.ResolveCostFromResult computes it via the
	// modelcatalog (same convention as cliexec).
	result.Cost = result.Tokens.Cost

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		result.Status = "failed"
		result.Reason = "execution timeout"
		return result, nil
	case bootErr != nil:
		// Boot wraps with ErrBootFailed for any setup failure (loopback,
		// boot dir, workspace, runtime) — surface the message directly.
		result.Status = "failed"
		result.Reason = bootErr.Error()
		if streamErr != nil {
			result.Reason = streamErr.Error() + " | " + result.Reason
		}
		return result, nil
	case sess == nil:
		// Defensive — Boot returns either (sess, nil) or (nil, err); this
		// branch shouldn't fire but keeps the type-switch honest.
		result.Status = "failed"
		result.Reason = "agent.Boot returned nil session without error"
		return result, nil
	case sess.Status == StatusDone && (sess.ExitCode == nil || *sess.ExitCode == 0):
		result.Status = "done"
		return result, nil
	default:
		exit := -1
		if sess.ExitCode != nil {
			exit = *sess.ExitCode
		}
		reasons := make([]string, 0, 2)
		if streamErr != nil {
			reasons = append(reasons, streamErr.Error())
		}
		reasons = append(reasons, fmt.Sprintf("exit %d", exit))
		result.Status = "failed"
		result.Reason = strings.Join(reasons, " | ")
		return result, nil
	}
}

// optsFromJob projects an executor.ExecutionJob onto agent.Options for the
// ModeOneShot dispatch path. Preserves cliexec's behavior:
//
//   - Workdir comes from the resolved working dir (not the raw job.WorkingDir).
//   - SystemPrompt = job.SystemPrompt (agent_file persona stacks on top inside
//     composeSystemPrompt at Boot time).
//   - Description carries through as the user-prompt body for the OneShot turn.
//   - Env / Metadata pass through.
func optsFromJob(job *executor.ExecutionJob, resolvedWD string) Options {
	return Options{
		Mode:         ModeOneShot,
		AgentProfile: job.AgentProfile,
		Workdir:      resolvedWD,
		ProjectID:    "", // ExecutionJob doesn't currently carry ProjectID
		TaskID:       job.TaskID,
		RunID:        job.RunID,
		SystemPrompt: job.SystemPrompt,
		AgentFile:    job.AgentFile,
		Env:          job.Environment,
		Description:  job.Description,
		Metadata:     job.Metadata,
	}
}

// translateStreamEvent maps a single provider.StreamEvent onto:
//   - the ExecutionEvent callback (delta → log, tool_use, usage → token-event)
//   - the result accumulator (tokens, cost — clockwork-side cost computation
//     happens in the scheduler from these tokens; the executor just reports raw)
//   - the streamErr sink (turn-terminal EventError)
//
// Forked verbatim from cliexec/cliexec.go's translateStreamEvent.
func translateStreamEvent(
	ev provider.StreamEvent,
	result *executor.ExecutionResult,
	cb executor.EventCallback,
	onError func(string),
) {
	switch ev.Type {
	case provider.EventDelta:
		if cb != nil && ev.Content != "" {
			cb(executor.LogEvent(ev.Content))
		}
	case provider.EventToolUse:
		if ev.ToolUse != nil && cb != nil {
			cb(executor.ToolUseEvent(ev.ToolUse.Name, summarizeToolInput(ev.ToolUse)))
		}
	case provider.EventUsage:
		if ev.Usage == nil {
			return
		}
		result.Tokens.PromptTokens += ev.Usage.InputTokens
		result.Tokens.CompletionTokens += ev.Usage.OutputTokens
		if cb != nil {
			cb(executor.TokenEvent(ev.Usage.InputTokens, ev.Usage.OutputTokens, 0))
		}
	case provider.EventError:
		if ev.Error != "" && onError != nil {
			onError(ev.Error)
		}
		if cb != nil {
			cb(executor.LogEvent("[error] " + ev.Error))
		}
	}
}

// summarizeToolInput renders a short, secret-sanitized preview of a tool
// invocation's args for the UI. Forked verbatim from cliexec.
func summarizeToolInput(tu *provider.ToolUseBlock) string {
	if tu == nil || len(tu.Input) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tu.Input))
	for k, v := range tu.Input {
		if executor.LooksLikeSecret(k) {
			continue
		}
		s := fmt.Sprintf("%v", v)
		if len(s) > 80 {
			s = s[:77] + "..."
		}
		parts = append(parts, k+"="+s)
		if len(parts) >= 3 {
			break
		}
	}
	return strings.Join(parts, " ")
}
