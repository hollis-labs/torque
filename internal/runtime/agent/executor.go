package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/toolbroker"
)

// Executor adapts agent.Boot to the executor.Executor contract used by the
// scheduler's worker pool. Registered as "cli" — the slot the legacy
// cliexec.CLIExecutor occupied.
//
// Dual-mode dispatch (CW-20260519-0095). The Mode picked per job is derived
// from job.Kind, not hardcoded:
//
//   - kind=agent  → ModeLongLived. Worker stays resident; completion is
//     signaled by the worker self-transitioning its task to `review` (or
//     `blocked`). Idle reap fires on heartbeat staleness (default 30m, per-
//     task override metadata.inactivity_threshold_seconds). A hard ceiling
//     (default 12h, metadata.hard_ceiling_seconds) is the safety net.
//
//   - anything else → ModeOneShot. Single turn, auto-stop on return. The
//     planner / reviewer-end-agent (kind=internal) and any bounded-mechanical
//     callers keep the legacy lifecycle.
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
//
// SupportsResume is true at the executor level because the cli executor
// dispatches to multiple providers and at least one (claude, codex) supports
// native resume. Per-task dispatch — which provider is actually used for a
// given job — must consult ProviderCapabilities(profile.Provider) for the
// accurate per-adapter answer (CW-20260512-0059, sprint α decision D4).
func (e *Executor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{
		SupportsStreaming:   true,
		SupportsTools:       true,
		SupportsSandbox:     false,
		SupportsPermissions: false,
		SupportsResume:      true,
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
	// Resolve the kind via the same matrix Boot uses (profile override →
	// per-provider default). Validate then exercises adapterFor with the
	// resolved kind so unsupported provider/kind combinations surface as
	// permanent errors at enqueue, not as boot-time failures.
	kind, kindErr := selectRuntimeKind(profile.Provider, profile.RuntimeKind)
	if kindErr != nil {
		return executor.NewPermanentError(kindErr)
	}
	if _, _, err := adapterFor(profile, job.AgentProfile, kind); err != nil {
		return executor.NewPermanentError(err)
	}
	resolvedWD, err := executor.ResolveWorkingDir(job.WorkingDir)
	if err != nil {
		return executor.NewPermanentError(fmt.Errorf("resolve working_dir: %w", err))
	}
	// A well-shaped working_dir that points at a missing path or a
	// non-directory is also permanent — the path is recorded on the task
	// and no retry can make a file system entry appear. Empty resolvedWD
	// is left to Run() to surface, mirroring the existing "blocked: working_dir
	// is required" path so we don't change behavior for executors that
	// dispatch with project_context-only working_dir resolution.
	if resolvedWD != "" {
		if err := executor.RequireExistingDir(resolvedWD); err != nil {
			return executor.NewPermanentError(fmt.Errorf("working_dir %q: %w", job.WorkingDir, err))
		}
	}
	return nil
}

// Run executes the job. Internally:
//
//  1. Resolves the working dir (PermanentError on bad shape).
//  2. Picks a Mode from job.Kind (kind=agent → ModeLongLived; else ModeOneShot).
//  3. ModeOneShot: Boot drives Start + SendInput + Stop synchronously; events
//     flow through eventFanout into the translation goroutine.
//  4. ModeLongLived: Boot returns once Start succeeds; runLongLived blocks
//     on the worker's completion signal (task self-transition out of doing),
//     heartbeat-driven idle reap, or hard ceiling — whichever fires first.
//  5. Returns an ExecutionResult shaped per the lifecycle manager's contract
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

	mode := modeForJob(job)
	opts := optsFromJob(job, resolvedWD)
	opts.Mode = mode
	profile := config.GetProfileOrDefault(e.deps.Profiles, job.AgentProfile)

	switch mode {
	case ModeLongLived:
		return e.runLongLived(ctx, profile, opts, cb)
	default:
		return e.runOneShot(ctx, profile, opts, cb)
	}
}

// runOneShot drives the legacy single-turn dispatch lifecycle: a per-job
// timeout, an event-fanout drain goroutine that accumulates token usage and
// forwards events to cb, then Boot blocking on the turn completing. Forked
// from the pre-CW-20260519-0095 Run body — no behavior change for this Mode.
func (e *Executor) runOneShot(ctx context.Context, profile config.AgentProfile, opts Options, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	const fanoutDepth = 64
	fanout := make(chan llmtypes.StreamEvent, fanoutDepth)

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

	opts = opts.withEventFanout(fanout)
	timeout := resolveTimeout(profile, opts)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sess, bootErr := Boot(runCtx, e.deps, opts)

	close(fanout)
	fanoutWG.Wait()

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		result.Status = "failed"
		result.Reason = "execution timeout"
		return result, nil
	case bootErr != nil:
		result.Status = "failed"
		result.Reason = bootErr.Error()
		if streamErr != nil {
			result.Reason = streamErr.Error() + " | " + result.Reason
		}
		return result, nil
	case sess == nil:
		result.Status = "failed"
		result.Reason = "agent.Boot returned nil session without error"
		return result, nil
	case sess.Status == StatusDone && (sess.ExitCode == nil || *sess.ExitCode == 0):
		result.Status = "done"
		zero := 0
		result.ExitCode = &zero
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
		result.ExitCode = &exit
		return result, nil
	}
}

// modeForJob picks the dispatch Mode from the job's Kind. CW-20260519-0095
// fixed the previous "always ModeOneShot" default, which silently truncated
// multi-turn kind=agent tickets at the end of one turn (the worker auto-
// stopped on end_of_turn and the task was left a zombie). kind=internal
// (planner, reviewer-end-agent) and the empty / unrecognized cases stay on
// ModeOneShot — those callers are bounded-mechanical by design.
//
// New Kind values get an explicit branch here; the default is conservative
// (ModeOneShot) so adding a new kind doesn't accidentally promote it to
// long-lived workers.
func modeForJob(job *executor.ExecutionJob) Mode {
	if job == nil {
		return ModeOneShot
	}
	switch job.Kind {
	case "agent":
		return ModeLongLived
	default:
		return ModeOneShot
	}
}

// optsFromJob projects an executor.ExecutionJob onto agent.Options for the
// ModeOneShot dispatch path. Preserves cliexec's behavior:
//
//   - Workdir comes from the resolved working dir (not the raw job.WorkingDir).
//   - RepoRoot carries the canonical checkout (job.RepoRoot) when the scheduler
//     resolved a per-run worktree; empty otherwise, leaving Boot's fallback to
//     Workdir in effect (shared mode, work_root == repo_root).
//   - SystemPrompt = job.SystemPrompt (agent_file persona stacks on top inside
//     composeSystemPrompt at Boot time).
//   - Description carries through as the user-prompt body for the OneShot turn.
//   - Env / Metadata pass through.
func optsFromJob(job *executor.ExecutionJob, resolvedWD string) Options {
	return Options{
		Mode:         ModeOneShot,
		AgentProfile: job.AgentProfile,
		Workdir:      resolvedWD,
		RepoRoot:     job.RepoRoot,
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

// translateStreamEvent maps a single llmtypes.StreamEvent onto:
//   - the ExecutionEvent callback (delta → log, tool_use, usage → token-event)
//   - the result accumulator (tokens, cost — torque-side cost computation
//     happens in the scheduler from these tokens; the executor just reports raw)
//   - the streamErr sink (turn-terminal EventError)
//
// Forked verbatim from cliexec/cliexec.go's translateStreamEvent.
func translateStreamEvent(
	ev llmtypes.StreamEvent,
	result *executor.ExecutionResult,
	cb executor.EventCallback,
	onError func(string),
) {
	switch ev.Type {
	case llmtypes.EventDelta:
		if cb != nil && ev.Content != "" {
			cb(executor.LogEvent(ev.Content))
		}
	case llmtypes.EventToolUse:
		if ev.ToolUse != nil && cb != nil {
			cb(executor.ToolUseEvent(ev.ToolUse.Name, summarizeToolInput(ev.ToolUse)))
		}
	case llmtypes.EventUsage:
		if ev.Usage == nil {
			return
		}
		result.Tokens.PromptTokens += ev.Usage.InputTokens
		result.Tokens.CompletionTokens += ev.Usage.OutputTokens
		if cb != nil {
			cb(executor.TokenEvent(ev.Usage.InputTokens, ev.Usage.OutputTokens, 0))
		}
	case llmtypes.EventError:
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
func summarizeToolInput(tu *llmtypes.ToolUseBlock) string {
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
