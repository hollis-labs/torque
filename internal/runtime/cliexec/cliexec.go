// Package cliexec implements the wrapper-driven CLI executor introduced by
// CW-20260427-0040. It composes the portfolio CLI substrate libraries —
// go-providers (CLIAdapter registry), go-sandbox (Profile + Apply, currently
// disabled until per-task wiring lands), go-runner (subprocess driver), and
// go-agent-sessions (Manager + per-turn lifecycle) — into a single executor
// that satisfies clockwork's executor.Executor contract.
//
// The legacy CLOCKWORK_* stdout-text signal protocol is gone. Lifecycle is
// observed off process state (clean exit → review, nonzero / timeout →
// blocked); interpretive signals (summary, blocked-with-reason, review-with-
// reason, artifact, comment, subtodo) ride on MCP tool calls — currently via
// the global `clockwork mcp` server with explicit task_id, until the per-task
// loopback FD wiring lands (CW-20260427-0059).
package cliexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
)

// CLIExecutor is the cliexec implementation of executor.Executor. One
// instance is shared across all CLI tasks; goroutine-safe.
//
// svc is the service-layer handle used to construct per-task MCP loopback
// adapters (CW-20260427-0059). When nil, the loopback wiring is skipped and
// agents fall back to the global `clockwork mcp` server with explicit task_id
// — same posture as Phase B's Path C landing. Production callsites supply a
// non-nil service via bootstrap.Executors.
type CLIExecutor struct {
	profiles config.ProfileMap
	svc      *service.Service
}

// Compile-time check that CLIExecutor satisfies the Executor interface.
var _ executor.Executor = (*CLIExecutor)(nil)

// New constructs a CLIExecutor with the given agent-profile registry and
// service handle. svc may be nil for tests that don't exercise the loopback;
// production callers (bootstrap.Executors) supply a real *service.Service so
// every task gets its own ephemeral MCP loopback.
func New(profiles config.ProfileMap, svc *service.Service) *CLIExecutor {
	return &CLIExecutor{profiles: profiles, svc: svc}
}

// Name returns the executor's registered name. The legacy executor-cli plugin
// occupied "cli"; cliexec inherits the slot since it replaces it wholesale.
func (e *CLIExecutor) Name() string { return "cli" }

// Capabilities reports what cliexec supports. SupportsSandbox stays false in
// CW-20260427-0059: go-sandbox's macOS SBPL builder maps Net=false to a
// blanket `(deny network*)` that blocks loopback (127.0.0.1) too, which would
// strand the per-task MCP loopback HTTP server. Sandbox restoration moves to
// a follow-up Phase F that depends on a go-sandbox AllowLoopback knob (or
// Net=true profiles, accepting FS-only scoping). Tracked in the impl-report
// known-limitations.
func (e *CLIExecutor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{
		SupportsStreaming:   true,
		SupportsTools:       false,
		SupportsSandbox:     false,
		SupportsPermissions: false,
	}
}

// Validate rejects jobs the executor cannot run. A missing-or-unloaded agent
// profile is config-permanent (PermanentError → block-no-retry per
// CW-20260418-0010); unknown providers are likewise permanent.
func (e *CLIExecutor) Validate(job *executor.ExecutionJob) error {
	if err := job.Validate(); err != nil {
		return err
	}
	profile := config.GetProfileOrDefault(e.profiles, job.AgentProfile)
	if _, _, err := adapterFor(profile, job.AgentProfile); err != nil {
		return executor.NewPermanentError(err)
	}
	if _, err := executor.ResolveWorkingDir(job.WorkingDir); err != nil {
		return executor.NewPermanentError(fmt.Errorf("resolve working_dir: %w", err))
	}
	return nil
}

// Run executes the job: spawns the agent CLI under go-runner via
// go-agent-sessions, captures stderr to a sidecar, surfaces stream events
// (deltas, tool-use, usage) through the EventCallback, and returns an
// ExecutionResult shaped for the lifecycle manager. No CLOCKWORK_* parsing.
func (e *CLIExecutor) Run(ctx context.Context, job *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	profile := config.GetProfileOrDefault(e.profiles, job.AgentProfile)

	agent, err := loadAgentFile(job)
	if err != nil {
		return &executor.ExecutionResult{Status: "blocked", Reason: err.Error()}, nil
	}
	if agent != nil && len(agent.Tools) > 0 {
		log.Printf("cliexec: agent file declares tools=%v (advisory only; no enforcement) task=%s", agent.Tools, job.TaskID)
	}

	cliAdapter, caps, err := adapterFor(profile, job.AgentProfile)
	if err != nil {
		return &executor.ExecutionResult{Status: "failed", Reason: err.Error()}, nil
	}

	resolvedWD, err := executor.ResolveWorkingDir(job.WorkingDir)
	if err != nil {
		return &executor.ExecutionResult{Status: "blocked", Reason: fmt.Sprintf("resolve working_dir: %v", err)},
			executor.NewPermanentError(fmt.Errorf("resolve working_dir: %w", err))
	}
	if resolvedWD == "" {
		return &executor.ExecutionResult{Status: "blocked", Reason: "working_dir is required for cliexec"},
			executor.NewPermanentError(errors.New("cliexec: working_dir is required"))
	}

	timeout := resolveTimeout(profile, job)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	systemPrompt := composeSystemPrompt(job, agent)
	prompt := composePrompt(job)

	// CW-20260427-0059: per-task MCP loopback + boot dir.
	//
	// setupLoopback returns (nil, nil) when e.svc is nil (test path). When
	// non-nil, the listener is bound and the serve goroutine is running before
	// we touch the boot dir; defer Shutdown drains the goroutine and closes
	// the listener after the spawned agent exits.
	loopback, err := setupLoopback(e.svc, job.TaskID)
	if err != nil {
		return &executor.ExecutionResult{Status: "failed", Reason: fmt.Sprintf("loopback setup: %v", err)}, nil
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		if err := loopback.Shutdown(shutdownCtx); err != nil {
			log.Printf("cliexec: loopback shutdown task=%s run=%d: %v", job.TaskID, job.RunID, err)
		}
	}()

	// Boot dir is the spawned agent's cwd. The project dir (resolvedWD) is
	// reachable via --add-dir for adapters that support it (currently claude).
	// When loopback is nil (test path), fall back to spawning in resolvedWD —
	// matches Phase B's pre-loopback behavior so unit tests don't have to
	// mock a service.
	spawnWD := resolvedWD
	var bootDir string
	if loopback != nil {
		bootDir, err = setupBootDir(job.TaskID, job.RunID, systemPrompt, resolvedWD, loopback.Port())
		if err != nil {
			return &executor.ExecutionResult{Status: "failed", Reason: fmt.Sprintf("boot dir setup: %v", err)}, nil
		}
		spawnWD = bootDir
		defer func() {
			if err := os.RemoveAll(bootDir); err != nil {
				log.Printf("cliexec: boot dir cleanup task=%s run=%d path=%s: %v", job.TaskID, job.RunID, bootDir, err)
			}
		}()
	}

	// Override BuildArgs so we can prepend profile.Args (e.g.
	// --dangerously-skip-permissions) and append --model when set. Adapters
	// that don't accept --model silently fail at run-time; profiles SHOULD
	// avoid setting Model for those providers.
	//
	// When the boot dir pattern is active, the agent's cwd is bootDir and
	// the project root is reachable only via the adapter's "additional dir"
	// flag (--add-dir for claude). Other adapters' equivalents are per-CLI
	// follow-ups; without them the agent has no project access.
	buildArgs := func(turnPrompt, sessionID string) []string {
		args := cliAdapter.BuildArgs(turnPrompt, systemPrompt, sessionID)
		if profile.Model != "" {
			args = append(args, "--model", profile.Model)
		}
		if bootDir != "" && cliAdapter.Name() == "claude" {
			args = append(args, "--add-dir", resolvedWD)
		}
		if filtered := profileArgsExcludingDevFlag(profile); len(filtered) > 0 {
			args = append(filtered, args...)
		}
		return args
	}

	rt, err := agentsessions.NewFromAdapter(agentsessions.AdapterRuntimeConfig{
		ID:        "clockwork-cli/" + cliAdapter.Name(),
		Kind:      "cli",
		Adapter:   cliAdapter,
		Caps:      caps,
		BuildArgs: buildArgs,
		WaitDelay: cancelGraceFromEnv(),
	})
	if err != nil {
		return &executor.ExecutionResult{Status: "failed", Reason: err.Error()}, err
	}
	if err := rt.Prepare(runCtx); err != nil {
		return &executor.ExecutionResult{Status: "failed", Reason: err.Error()}, nil
	}

	// Stderr sidecar (preserves CW-20260417-0024 behavior).
	stderrWriter, stderrTail, closeStderr := openStderrSidecar(job.RunID)
	defer closeStderr()

	env := buildEnv(profile, job, agent)

	// EventFanout streams parsed provider events from go-agent-sessions →
	// our ExecutionEvent translator. 64-deep so a slow consumer doesn't
	// block the runner.
	eventFanout := make(chan provider.StreamEvent, 64)

	mgr := agentsessions.NewManager(nil) // no StateSink — clockwork's lifecycle manager owns FSM

	sessID := fmt.Sprintf("cliexec-%s-%d", job.TaskID, job.RunID)
	startErr := mgr.Start(runCtx, agentsessions.StartRequest{
		ID:      sessID,
		Runtime: rt,
		Options: agentsessions.StartOptions{
			// CW-20260427-0059: spawnWD is the per-task boot dir when the
			// loopback is wired (production path). Falls back to resolvedWD
			// when e.svc is nil (test path) — matches Phase B behavior.
			Workdir:     spawnWD,
			Env:         env,
			Stderr:      stderrWriter,
			EventFanout: eventFanout,
			// Profile: zero-value. Sandbox restoration deferred to Phase F —
			// go-sandbox's Net=false denies loopback (127.0.0.1), needs an
			// AllowLoopback knob lib-side before we can populate this.
		},
	})
	if startErr != nil {
		close(eventFanout)
		return &executor.ExecutionResult{
			Status: "failed",
			Reason: failureReason(stderrTail, startErr, nil),
		}, nil
	}

	// Forward stream events → ExecutionEvent callback. Aggregates token
	// usage so the scheduler's cost ledger sees real numbers
	// (cost_source=executor) for runs that emit Usage.
	result := &executor.ExecutionResult{}
	var (
		fanoutWG       sync.WaitGroup
		streamErr      error
		streamErrOnce  sync.Once
	)
	fanoutWG.Add(1)
	go func() {
		defer fanoutWG.Done()
		for ev := range eventFanout {
			translateStreamEvent(ev, result, cb, func(err string) {
				streamErrOnce.Do(func() { streamErr = errors.New(err) })
			})
		}
	}()

	// SendInput drives runner.Run synchronously for one turn. Returns when
	// the spawned process exits (cleanly or otherwise) or runCtx is done.
	sendErr := mgr.SendInput(sessID, []byte(prompt))

	// Tear down the session and wait for terminal state.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	_ = mgr.Stop(stopCtx, sessID)

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	exitCode, _ := mgr.WaitSession(waitCtx, sessID)

	// Drain the fanout channel. The session's read goroutine writes to it
	// from inside SendInput; once SendInput returns, no more events arrive.
	close(eventFanout)
	fanoutWG.Wait()

	// Tokens were populated by translateStreamEvent. Cost is left at zero —
	// scheduler.cost.ResolveCostFromResult will compute it via modelcatalog
	// (cost_source=models_dev) since adapters don't currently emit cost.
	result.Cost = result.Tokens.Cost

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		result.Status = "failed"
		result.Reason = "execution timeout"
		return result, nil
	case sendErr != nil:
		result.Status = "failed"
		result.Reason = failureReason(stderrTail, sendErr, streamErr)
		log.Printf("cliexec: send-input failure (task=%s run=%d): %s", job.TaskID, job.RunID, result.Reason)
		return result, nil
	case streamErr != nil && exitCode != 0:
		result.Status = "failed"
		result.Reason = failureReason(stderrTail, fmt.Errorf("exit %d", exitCode), streamErr)
		log.Printf("cliexec: stream-error + nonzero exit (task=%s run=%d exit=%d): %s",
			job.TaskID, job.RunID, exitCode, result.Reason)
		return result, nil
	case exitCode == 0:
		// Clean exit: lifecycle manager applies OnDone (default → review).
		// If the agent already drove an interpretive transition (blocked /
		// review via MCP), the lifecycle manager observes the task is no
		// longer in `doing` and either skips (terminal status) or attempts
		// the configured OnDone transition (no-op if already there).
		result.Status = "done"
		return result, nil
	default:
		result.Status = "failed"
		result.Reason = failureReason(stderrTail, fmt.Errorf("exit %d", exitCode), streamErr)
		log.Printf("cliexec: nonzero exit (task=%s run=%d exit=%d): %s",
			job.TaskID, job.RunID, exitCode, result.Reason)
		return result, nil
	}
}

// translateStreamEvent maps a single provider.StreamEvent onto:
//   - the ExecutionEvent callback (delta → log, tool_use, usage → token-event)
//   - the result accumulator (tokens, cost — clockwork-side cost computation
//     happens in the scheduler from these tokens; cliexec just reports raw)
//   - the streamErr sink (turn-terminal EventError)
//
// Cost is left at zero in cliexec — scheduler.cost.ResolveCostFromResult will
// compute it from result.Tokens via the modelcatalog. cost_source=executor
// requires a non-zero Tokens.Cost on the result; cliexec passes through any
// adapter-reported cost (currently none — Claude doesn't emit cost) and the
// scheduler's models.dev backfill takes over.
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
// invocation's args for the UI. Keep it small — the full payload is captured
// by other channels (run_events, sidecar). Returns empty when the input is
// empty or unrenderable.
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

// profileArgsExcludingDevFlag returns profile.Args with
// --dangerously-skip-permissions stripped. The dev flag is consumed by
// adapterFor to pick NewClaudeAdapterDev; passing it through profile.Args
// would double-add the flag.
func profileArgsExcludingDevFlag(profile config.AgentProfile) []string {
	if len(profile.Args) == 0 {
		return nil
	}
	out := make([]string, 0, len(profile.Args))
	for _, a := range profile.Args {
		if a == "--dangerously-skip-permissions" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// failureReason picks the most informative diagnostic available for a failed
// run: stderr tail if present, else a stream-level error, else the wait
// error's text. Empty string only when all sources are silent.
func failureReason(stderrTail *bytes.Buffer, waitErr, streamErr error) string {
	if tail := tailString(stderrTail, stderrTailBytes); tail != "" {
		return tail
	}
	if streamErr != nil && streamErr.Error() != "" {
		return streamErr.Error()
	}
	if waitErr != nil {
		return waitErr.Error()
	}
	return ""
}
