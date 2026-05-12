package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-sandbox/sandbox"
)

// Boot is the unified entry point for spawning an agent session. Replaces
// cliexec.Run + sessionmgr.Manager.Launch + the planstart SendInput dance.
// Per-task tempdir + planted system prompt + MCP loopback + env composition
// fire for every Mode value — the lifecycle policy (long-lived vs one-shot
// vs subagent etc) drives the StartOptions tuning, not the setup pipeline.
//
// Returns the persisted *Session on success. For ModeOneShot the call blocks
// until the single turn completes (Stop fires inline); for the other modes
// the call returns once Manager.Start has accepted the request and the
// kickoff (when applicable) is in flight via AutoFireFirstTurn.
//
// On error: any partially-created boot dir is os.RemoveAll'd, the loopback
// (if it was opened) is shut down, and any session row created in this call
// is marked failed before the error returns.
func Boot(ctx context.Context, deps *Dependencies, opts Options) (*Session, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if deps == nil || deps.Store == nil || deps.Sessions == nil {
		return nil, fmt.Errorf("agent.Boot: deps.Store and deps.Sessions are required")
	}

	mgr := deps.Sessions
	if err := mgr.checkStopped(); err != nil {
		return nil, err
	}

	profile := config.GetProfileOrDefault(deps.Profiles, opts.AgentProfile)

	// PTY decision drives BOTH Caps.PTY and the adapter constructor — claude
	// has different arg shapes per mode (go-providers v0.8.1 PTY-aware
	// ClaudeAdapter), so adapterFor needs to know the decision. Computing
	// PTY first keeps the adapter wiring in lockstep with the runtime caps.
	pty := shouldUsePTY(opts.Mode, profile.Provider, profile.PTY, opts.SubprocessPerTurnOverride)

	cliAdapter, baseCaps, err := adapterFor(profile, opts.AgentProfile, pty)
	if err != nil {
		return nil, fmt.Errorf("%w: adapter: %v", ErrAdapterNotFound, err)
	}
	caps := baseCaps
	caps.PTY = pty

	// Session ID + role (used for SessionMeta + boot.md content).
	idFn := opts.IDFn
	if idFn == nil {
		idFn = mgr.idFn
	}
	sessID := idFn()
	role := opts.Role
	if role == "" {
		role = opts.AgentProfile
	}

	// Agent file (resolves + parses; nil when Options.AgentFile is empty).
	agentFile, err := loadAgentFile(opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBootFailed, err)
	}

	// System prompt = agent_file persona + Options.SystemPrompt + inherited
	// project context. Planted into CLAUDE.md / AGENTS.md / agents/<name>.md
	// via the lib's BootDirSpec; the LLM sees it on cwd-load.
	systemPrompt := composeSystemPrompt(opts, agentFile)

	// MCP loopback (closure-bound to taskID). nil-builder disables (test path).
	// Role flows in so the builder can pick the appropriate MCP tool surface:
	// kind=agent workers get the restricted self-task subset; orchestrator-
	// class roles (orchestrator / planner / reviewer-end-agent) get the full
	// cross-task surface (CW-20260509-0018).
	loopback, err := setupLoopback(deps.Loopback, opts.TaskID, role)
	if err != nil {
		return nil, fmt.Errorf("%w: setup loopback: %v", ErrBootFailed, err)
	}

	// Bare-mode apiKeyHelper (CW-20260509-0016, go-providers v0.9.2):
	// thread Dependencies.ApiKeyHelperPath onto the adapter BEFORE
	// plantBootDir runs, because the .claude/settings.json Render
	// closure captures the receiver at render time and emits
	// `apiKeyHelper: <path>` only when the field is non-empty. Setting
	// it post-plant would leave the planted file with no helper field
	// and bare mode would fall back to ANTHROPIC_API_KEY only — which
	// is exactly the gap this closes for subscription users (no env
	// key, authenticated via `claude` interactive → keychain).
	//
	// Non-claude / non-bare adapters skip this branch via the type-
	// assertion + Bare check; the adapter type-assert is repeated below
	// for the four spawn-arg-injection fields, which legitimately need
	// the post-plant layout paths.
	if claudeAdapter, ok := cliAdapter.(*provider.ClaudeAdapter); ok && claudeAdapter.Bare && deps.ApiKeyHelperPath != "" {
		// Validate the resolved path is still an executable file at the
		// moment we're about to thread it into the adapter. Catches the
		// race where the helper was removed between resolveApiKeyHelperPath
		// at startup and the first dispatch (rare but recoverable). Invalid
		// path → log + skip the adapter set; bare mode falls back to
		// ANTHROPIC_API_KEY in env (existing CW-20260509-0011 contract).
		// Failing fast vs. degrading gracefully: degrade. A missing helper
		// shouldn't block a dispatch on systems where the env-key path
		// works fine — operators will see the misconfig in the log line.
		if isApiKeyHelperExecutable(deps.ApiKeyHelperPath) {
			claudeAdapter.ApiKeyHelperPath = deps.ApiKeyHelperPath
		} else {
			log.Printf("agent.Boot: deps.ApiKeyHelperPath=%q no longer points at an executable file; skipping (bare-mode claude will rely on ANTHROPIC_API_KEY)", deps.ApiKeyHelperPath)
		}
	}

	// Boot dir layout via per-provider plant. The lib's BootDirSpec covers
	// claude/codex/opencode end-to-end; gemini/copilot dispatch to the
	// bespoke planters in bootdir_<provider>.go (which currently fail with
	// ErrBootDirNotImplemented until probed).
	kickoffMD := kickoffMarkdown(opts, role)
	loopbackURL := ""
	if loopback != nil {
		loopbackURL = loopback.URL()
	}
	layout, err := plantBootDir(plantParams{
		Provider:       profile.Provider,
		Adapter:        cliAdapter,
		TaskID:         opts.TaskID,
		RunID:          opts.RunID,
		AgentName:      opts.AgentProfile,
		SystemPrompt:   systemPrompt,
		KickoffContent: kickoffMD,
		ProjectDir:     opts.Workdir,
		MCPLoopbackURL: loopbackURL,
		// CW-20260510-0110: thread daemon-scoped Mux config from
		// Dependencies onto every Boot. Empty MuxCommand (Mux not
		// resolved at startup) → bootdir plant emits no `mux` MCP
		// entry (existing pre-CW-20260510-0110 behavior preserved).
		MuxCommand: deps.MuxCommand,
		MuxArgs:    deps.MuxArgs,
		MuxEnv:     deps.MuxEnv,
	})
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: plant boot dir: %v", ErrBootFailed, err)
	}

	// Bare-mode claude (go-providers v0.9.0): populate the four explicit-
	// injection paths from the planted layout. BuildArgs reads these fields
	// to emit --mcp-config / --append-system-prompt-file / --settings /
	// --add-dir; without this step the spawn omits those flags and falls
	// back to auto-discovery (which bare mode disables — the agent then has
	// no MCP, no system prompt file, no settings).
	//
	// Why bare mode: Anthropic's recommended shape for scripted/SDK calls;
	// skips auto-discovery of ~/.claude/settings.json, ~/.claude.json,
	// hooks/plugins/MCP/OAuth/keychain/CLAUDE.md auto-find. Obsoletes the
	// operator-config-bleed-through class (CW-20260508-0019) for bare
	// consumers. Non-claude / non-bare adapters fall through silently via
	// the type-assertion check.
	claudeBareAdapter, claudeIsBare := cliAdapter.(*provider.ClaudeAdapter)
	if claudeIsBare && claudeBareAdapter.Bare {
		inj := claudeBareAdapter.BareInjectionPaths(layout.BootDir, opts.Workdir)
		claudeBareAdapter.MCPConfigPath = inj.MCPConfigPath
		claudeBareAdapter.AppendSystemPromptFile = inj.AppendSystemPromptFile
		claudeBareAdapter.SettingsPath = inj.SettingsPath
		claudeBareAdapter.ProjectDir = inj.ProjectDir
	} else {
		claudeIsBare = false
	}

	// Workspace dir (persistent state + logs). Independent of boot dir.
	ws, err := workspaceCreate(deps.WorkspacesRoot, opts.ProjectID, sessID)
	if err != nil {
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: workspace: %v", ErrBootFailed, err)
	}

	// Env: base (filtered OS + CLOCKWORK_TASK_ID/RUN_ID + agent-file env +
	// opts.Env) + per-provider amendments (e.g. OPENCODE_CONFIG_DIR).
	env := composeEnv(profile, opts, agentFile)
	env = append(env, layout.EnvAmendments...)

	// BuildArgs wrapper: prepends profile.Args (minus dev-mode flag, which
	// is consumed by adapterFor → NewClaudeAdapterDev*), appends --model when
	// set, appends the lib's ProjectDirArg (e.g. --add-dir <projectDir>).
	//
	// Bare-mode claude exception: the lib's BuildArgs already emits
	// `--add-dir <projectDir>` from a.ProjectDir (populated above from
	// BareInjectionPaths), and the lib does NOT de-dupe args. Appending
	// layout.ProjectDirArg here would emit `--add-dir <projectDir>` twice.
	// Claude tolerates the double-add (later wins / both append to the
	// allow-list), but we skip the second emit to keep the argv clean and
	// consistent with the lib's bare-mode contract: in bare mode all four
	// explicit-injection flags flow through the adapter fields, not the
	// closure's spec-driven append.
	skipProjectDirArg := claudeIsBare

	// opencode's argv shape requires `--model <X>` BEFORE the positional
	// prompt arg (`opencode run --agent <A> --model <M> "<prompt>"`).
	// OpencodeAdapter.BuildArgs already emits the flag in that position
	// when adapter.Model is populated (factory.go threads profile.Model
	// onto the adapter). Appending the generic `--model` suffix here
	// would either land it AFTER the positional prompt (corrupt argv,
	// since opencode parses anything past the prompt as additional
	// message args) or duplicate the flag. Per-provider opt-out keeps
	// claude's existing trailing `--model` tolerant behavior intact.
	skipModelSuffix := skipModelSuffixForProvider(profile.Provider)

	buildArgs := func(turnPrompt, sessionID string) []string {
		return composeBuildArgs(buildArgsParams{
			Adapter:           cliAdapter,
			Profile:           profile,
			SystemPrompt:      systemPrompt,
			TurnPrompt:        turnPrompt,
			SessionID:         sessionID,
			ProjectDirArg:     layout.ProjectDirArg,
			SkipProjectDirArg: skipProjectDirArg,
			SkipModelSuffix:   skipModelSuffix,
		})
	}

	runtimeFactory := agentsessions.NewFromAdapter
	if deps.RuntimeFactory != nil {
		runtimeFactory = func(cfg agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			return deps.RuntimeFactory(cfg)
		}
	}
	runtime, err := runtimeFactory(agentsessions.AdapterRuntimeConfig{
		ID:        "clockwork-cli/" + cliAdapter.Name(),
		Kind:      "cli",
		Adapter:   cliAdapter,
		Caps:      caps,
		BuildArgs: buildArgs,
		WaitDelay: cancelGraceFromEnv(),
	})
	if err != nil {
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: construct runtime: %v", ErrBootFailed, err)
	}
	if err := runtime.Prepare(ctx); err != nil {
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: prepare runtime: %v", ErrBootFailed, err)
	}

	// Resume preset: ModeResume threads the provider's session ID via
	// SessionIDPreset. Lookup goes through the named checkpoint.
	var sessionIDPreset string
	var resumeHint []byte
	if opts.Mode == ModeResume {
		cp, err := findCheckpoint(deps.Store, opts.ResumeFromCheckpoint)
		if err != nil {
			_ = os.RemoveAll(layout.BootDir)
			shutdownLoopbackHandle(loopback)
			return nil, fmt.Errorf("%w: %v", ErrBootFailed, err)
		}
		if len(cp.ResumeHint) > 0 {
			sessionIDPreset = string(cp.ResumeHint)
			resumeHint = cp.ResumeHint
		}
	}

	// OnSessionID persists the provider's first observed session ID so
	// future Resume can re-thread it (Caps.ProviderSessionID adapters).
	var onSessionID func(string)
	if caps.ProviderSessionID && deps.Store != nil {
		onSessionID = func(id string) {
			_ = deps.UpdateSessionResumeHint(context.Background(), sessID, []byte(id))
		}
	}

	// Persist DB row in `launching` state. The lib's StateSink transitions
	// it through running → done/failed asynchronously via the watch
	// goroutine.
	//
	// Stamp substrate-internal keys into SessionMeta so Get/List can later
	// reconstitute Mode/BootDir/WorkspaceDir/ParentSessionID — fields that
	// don't have dedicated DB columns yet. Caller-supplied keys are merged
	// over the substrate values via the loop below; collisions favor the
	// substrate (caller can't override the locked Mode/BootDir/etc).
	persistedMeta := make(map[string]string, len(opts.SessionMeta)+4)
	for k, v := range opts.SessionMeta {
		persistedMeta[k] = v
	}
	persistedMeta[metaKeyMode] = opts.Mode.String()
	persistedMeta[metaKeyBootDir] = layout.BootDir
	persistedMeta[metaKeyWorkspaceDir] = ws.Root
	if opts.ParentSessionID != "" {
		persistedMeta[metaKeyParentSessionID] = opts.ParentSessionID
	}
	metaJSON, err := encodeMeta(persistedMeta)
	if err != nil {
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: encode session meta: %v", ErrBootFailed, err)
	}
	rec := &sqlstore.SessionRecord{
		ID:           sessID,
		AgentProfile: opts.AgentProfile,
		Provider:     profile.Provider,
		RuntimeID:    runtime.ID(),
		RuntimeKind:  runtime.Kind(),
		Workdir:      layout.SpawnCwd,
		ProjectID:    nullableString(opts.ProjectID),
		TaskID:       nullableString(opts.TaskID),
		State:        string(StatusLaunching),
		ResumeHint:   resumeHint,
		MetaJSON:     metaJSON,
	}
	if err := deps.CreateSession(context.Background(), rec); err != nil {
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: create session row: %v", ErrBootFailed, err)
	}

	// Sandbox profile: zero-value preserves "no sandbox" today (Phase F);
	// when opts supplies a profile, force AllowLoopback=true so per-task
	// MCP can reach 127.0.0.1.
	sandboxProfile := sandbox.Profile{}
	if opts.SandboxProfile != nil {
		sandboxProfile = *opts.SandboxProfile
		sandboxProfile.AllowLoopback = true
	}

	// AutoFireFirstTurn drives the kickoff via the lib's race-free Start path
	// for long-lived modes; ModeOneShot stays false so the executor wrapper
	// drives SendInput synchronously and gets one turn's exit code back. The
	// flag flows uniformly through StartOptions for ModeLongLived/Subagent/
	// Background — substrate consumers (orchestrator boot, subagent spawn,
	// background fire-and-forget) get a single declarative contract: "boot
	// fires the first turn for you" — without a Boot-side SendInput plumbing
	// path that would race the Launch (CW-20260507-0011's class of bug).
	autoFire := opts.Mode == ModeLongLived ||
		opts.Mode == ModeSubagent ||
		opts.Mode == ModeBackground
	var firstTurnPayload []byte
	if autoFire {
		firstTurnPayload = []byte(kickoffPayload(layout.KickoffFile))
	}

	// Stderr sidecar: forward to per-run sidecar log + tail buffer + the
	// session-scoped session.log (matches CW-20260417-0024 + CW-20260508-0006).
	// PTY runtime merges stderr into the tty stream at the kernel level, so
	// the sidecar only fires on the adapter (subprocess-per-turn) path.
	//
	// session.log tee (CW-20260508-0006) makes daemon-spawned subprocess
	// failures visible in the per-session workspace logs/session.log, where
	// forensic tooling looks first. Without this, RunID=0 boot paths (which
	// is every long-lived orchestrator session created via the MCP
	// session_create handler) wrote stderr to a per-run-id 0.stderr.log file
	// shared with every other RunID=0 spawn — easy to miss / overwrite, and
	// not where ops looks. session.log was previously only written by the
	// PTY runtime; the adapter (subprocess) path has its own tee here.
	var stderrWriter io.Writer = io.Discard
	closeStderr := func() {}
	if !caps.PTY {
		w, _, closer := openStderrSidecar(opts.RunID, ws.LogPath)
		stderrWriter = w
		closeStderr = closer
	}

	// Stream sidecar (CW-20260509-0001): persist llmtypes.StreamEvent values
	// (delta / tool_use / usage / error / done / session_id / thinking) to
	// <workspace>/logs/stream.jsonl. Pairs with the stderr→session.log tee
	// from CW-20260508-0006 — together they're the full forensic surface for
	// claude subprocess-per-turn sessions:
	//   - stream.jsonl captures the typed event stream (claude's stdout
	//     stream-json, projected through the lib's StreamEvent type). Error
	//     events that go to stdout in stream-json mode (and so don't land in
	//     session.log's stderr capture) show up here.
	//   - session.log captures stderr (signals, panics, MCP debug, "not
	//     logged in"-style claude-side bail messages).
	//
	// The stream fanout owns EventFanout in StartOptions for the adapter
	// (subprocess-per-turn) path: the drain goroutine consumes everything
	// the lib writes to it and persists each event as JSONL. When
	// opts.eventFanout is non-nil (executor's ModeOneShot path), we also
	// forward each event to it non-blockingly so the executor's
	// translateStreamEvent loop still gets its token-accounting + callback
	// fan-out.
	//
	// Guarded behind !caps.PTY for the same reason the stderr sidecar is:
	// the PTY runtime merges stream output into the tty stream at the kernel
	// level and routes events through the typed-event callback
	// (TypedEventCallback), not through the StreamEvent fanout. Wiring a
	// stream fanout for PTY would just produce an empty file and waste a
	// goroutine + fd. If the lib's PTY runtime starts emitting StreamEvent
	// values into EventFanout in the future, drop the !caps.PTY guard.
	const streamFanoutDepth = 64
	var streamFanout chan llmtypes.StreamEvent
	closeStreamFanout := func() {}
	if !caps.PTY {
		streamFanout, closeStreamFanout = startStreamFanout(ws.LogDir, streamFanoutDepth, opts.eventFanout)
	}

	// Supervisor + ResourceLimits resolution.
	// PTY runtime (go-agent-sessions v0.6.0) enforces these natively. Adapter
	// runtime forwards the fields but the lib silently ignores them pending
	// go-runner v0.3.x publishing the supervision API. profileSupervision
	// honors Options-supplied values regardless of PTY (so callers can set
	// them once; adapter-path enforcement lights up automatically when the
	// lib unblocks). Profile-config widening for default Supervisor +
	// ResourceLimits is a follow-up — today the profile branch returns
	// nil/nil unless explicitly overridden via Options.
	supervisor, limits := profileSupervision(profile, opts, caps.PTY)

	startReq := agentsessions.StartRequest{
		ID:      sessID,
		Runtime: runtime,
		Options: agentsessions.StartOptions{
			Workdir:            layout.SpawnCwd,
			WorkspaceDir:       ws.Root,
			LogPath:            ws.LogPath,
			BootPrompt:         systemPrompt,
			Env:                env,
			Stderr:             stderrWriter,
			Profile:            sandboxProfile,
			AttachEnabled:      true,
			AutoFireFirstTurn:  autoFire,
			FirstTurnPayload:   firstTurnPayload,
			SessionIDPreset:    sessionIDPreset,
			OnSessionID:        onSessionID,
			Supervisor:         supervisor,
			ResourceLimits:     limits,
			EventFanout:        streamFanout,
			TypedEventCallback: opts.TypedEventCallback,
		},
		SessionMeta: opts.SessionMeta,
	}

	// Context detachment for non-OneShot modes: long-lived sessions
	// (orchestrator/subagent/background/resume) must outlive the caller's
	// context. Without this, an HTTP request that triggers plan_start
	// has its r.Context() govern the orchestrator's first-turn subprocess —
	// when the client disconnects or times out (curl -m 60, browser nav,
	// SSE close, …), the cancellation propagates through Manager.Start →
	// adapterRuntime.Start → SendInput's synchronous AutoFireFirstTurn run
	// (from_adapter.go:127-138) → SIGTERM → 5s grace → SIGKILL. Surfaced
	// 2026-05-08 in S2.5 smoke as exit=1 in 60s flat with no stderr (signal
	// kill). ModeOneShot keeps the caller ctx because the executor wraps it
	// in context.WithTimeout(ctx, profile.timeout) and needs the cancel to
	// propagate for timeout enforcement.
	startCtx := ctx
	if opts.Mode != ModeOneShot {
		startCtx = context.WithoutCancel(ctx)
	}
	if err := mgr.inner.Start(startCtx, startReq); err != nil {
		closeStderr()
		closeStreamFanout()
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopbackHandle(loopback)
		// Inner.Start records StateFailed via StateSink on its own; no extra
		// row update needed here.
		return nil, fmt.Errorf("%w: %v", ErrBootFailed, err)
	}

	// Register per-session teardown hooks for non-OneShot modes. OneShot
	// runs synchronously below and drives its own teardown via Stop.
	// Without bootDir registration, long-lived/background/subagent/resume
	// sessions would leak $TMPDIR/clockwork-boot-* directories (carrying
	// .mcp.json with the loopback URL) until OS-level housekeeping ran.
	if opts.Mode != ModeOneShot {
		mgr.registerLoopback(sessID, loopback)
		mgr.registerStderrCloser(sessID, closeStderr)
		mgr.registerStreamCloser(sessID, closeStreamFanout)
		mgr.registerBootDir(sessID, layout.BootDir)
		// Per-session PID poller (CW-20260509-0008): the lib records pid only
		// at launch (always 0 for adapter-mode) and never refreshes the row's
		// last_activity between turns. The poller bridges that gap and drives
		// a clean Stop the moment Health.Alive flips false so orchestrators
		// finish in state=done instead of waiting for the next daemon-restart
		// sweep to mark the row crashed.
		mgr.registerPidPoller(sessID, startPidPoller(mgr, sessID, mgr.pidPollInterval))
	}

	sess := &Session{
		ID:              sessID,
		Mode:            opts.Mode,
		AgentProfile:    opts.AgentProfile,
		Provider:        profile.Provider,
		RuntimeID:       runtime.ID(),
		RuntimeKind:     runtime.Kind(),
		Workdir:         layout.SpawnCwd,
		BootDir:         layout.BootDir,
		WorkspaceDir:    ws.Root,
		ProjectID:       opts.ProjectID,
		TaskID:          opts.TaskID,
		ParentSessionID: opts.ParentSessionID,
		Status:          StatusLaunching, // updated on terminal observe
		Meta:            opts.SessionMeta,
		CreatedAt:       time.Now().UTC(),
	}

	// ModeOneShot drives the turn synchronously: SendInput delivers the
	// kickoff, Stop tears down, Wait surfaces the exit code.
	if opts.Mode == ModeOneShot {
		defer closeStderr()
		defer closeStreamFanout()
		defer shutdownLoopbackHandle(loopback)
		defer func() {
			if err := os.RemoveAll(layout.BootDir); err != nil {
				// Cleanup failure is non-fatal; the boot dir lives in
				// $TMPDIR and OS housekeeping reclaims it eventually.
				_ = err
			}
		}()

		prompt := composeUserPrompt(opts)
		if prompt == "" {
			prompt = kickoffPayload(layout.KickoffFile)
		}
		sendErr := mgr.inner.SendInput(sessID, []byte(prompt))

		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr.inner.Stop(stopCtx, sessID)
		stopCancel()

		waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		exitCode, _ := mgr.inner.WaitSession(waitCtx, sessID)
		waitCancel()

		sess.ExitCode = &exitCode
		switch {
		case sendErr != nil:
			sess.Status = StatusFailed
		case exitCode != 0:
			sess.Status = StatusFailed
		default:
			sess.Status = StatusDone
		}
	}

	return sess, nil
}

// findCheckpoint locates a checkpoint by ID via the store's direct lookup
// (sqlstore.Store.GetSessionCheckpoint). Replaces the prior 1:N scan over
// the most-recent 200 sessions which silently broke once the session list
// grew past the page limit (Copilot review feedback on PR #19).
func findCheckpoint(store *sqlstore.Store, checkpointID string) (*sqlstore.SessionCheckpointRecord, error) {
	if store == nil {
		return nil, fmt.Errorf("nil store")
	}
	if checkpointID == "" {
		return nil, ErrNoCheckpoint
	}
	cp, err := store.GetSessionCheckpoint(checkpointID)
	if err != nil {
		return nil, fmt.Errorf("get session checkpoint: %w", err)
	}
	if cp == nil {
		return nil, fmt.Errorf("%w: id=%s", ErrNoCheckpoint, checkpointID)
	}
	return cp, nil
}

// profileSupervision derives Supervisor + ResourceLimits from the profile
// + opts. Honors Options-supplied values regardless of ptyEnabled — the
// adapter path silently ignores them today (v0.6.0 lib gap pending
// go-runner v0.3.x publishing the supervision API), but callers can set
// them now and pickup is automatic when the lib unblocks. Pass-through
// keeps the API uniform across PTY / adapter Modes.
//
// Profile-config widening for default Supervisor / ResourceLimits is a
// separate follow-up — today the profile branch returns nil/nil unless
// the caller explicitly overrides via Options.Supervisor / Options.ResourceLimits.
func profileSupervision(profile config.AgentProfile, opts Options, ptyEnabled bool) (*agentsessions.SupervisorOptions, *agentsessions.ResourceLimits) {
	supervisor := opts.Supervisor
	limits := opts.ResourceLimits
	// Profile-config widening hook — currently a no-op. When the profile
	// shape grows Supervisor/ResourceLimits fields, default-fill here when
	// the caller didn't override.
	_ = profile
	_ = ptyEnabled
	return supervisor, limits
}

// buildArgsParams captures every input composeBuildArgs needs to
// assemble the per-turn argv for the lib's adapter Runtime. Extracted
// out of the buildArgs closure in Boot so the per-provider switches
// (skipModelSuffix, skipProjectDirArg) can be unit-tested directly.
type buildArgsParams struct {
	Adapter           provider.CLIAdapter
	Profile           config.AgentProfile
	SystemPrompt      string
	TurnPrompt        string
	SessionID         string
	ProjectDirArg     []string
	SkipProjectDirArg bool
	SkipModelSuffix   bool
}

// composeBuildArgs assembles the per-turn argv. It calls the
// adapter's BuildArgs for the provider-shape baseline, then optionally
// appends the generic --model suffix and the project-directory args
// (which are provider-specific: --add-dir for claude, --cd for codex,
// --dir for opencode — supplied by layout.ProjectDirArg), and finally
// prepends profile.Args (minus the dev-mode flag, which is consumed
// by adapterFor → NewClaudeAdapterDev*).
//
// Per-provider exceptions:
//
//   - skipModelSuffix=true: opencode's argv requires --model BEFORE
//     the positional prompt, which OpencodeAdapter.BuildArgs already
//     emits when adapter.Model is set (factory.go threads it). The
//     trailing-suffix path would either land --model AFTER the
//     prompt (argv corruption) or duplicate the flag.
//   - skipProjectDirArg=true: bare-mode claude already emits
//     --add-dir <projectDir> via a.ProjectDir; the lib doesn't
//     de-dupe, so appending layout.ProjectDirArg would double the
//     flag. Claude tolerates the duplicate but the cleaner path is
//     to suppress the second emit.
func composeBuildArgs(p buildArgsParams) []string {
	args := p.Adapter.BuildArgs(p.TurnPrompt, p.SystemPrompt, p.SessionID)
	if !p.SkipModelSuffix && p.Profile.Model != "" {
		args = append(args, "--model", p.Profile.Model)
	}
	if !p.SkipProjectDirArg && len(p.ProjectDirArg) > 0 {
		args = append(args, p.ProjectDirArg...)
	}
	if filtered := profileArgsExcludingDevFlag(p.Profile); len(filtered) > 0 {
		args = append(filtered, args...)
	}
	return args
}

// skipModelSuffixForProvider reports whether the generic --model
// suffix in composeBuildArgs should be suppressed for this provider.
//
// opencode is the lone case today: its argv requires --model BEFORE
// the positional prompt, and OpencodeAdapter.BuildArgs already emits
// the flag in the correct position when adapter.Model is populated
// (factory.go threads profile.Model onto the adapter). Claude / codex
// tolerate the trailing --model so they leave the suffix on.
//
// Adding a new provider here is a deliberate compatibility decision —
// most providers tolerate trailing --model and don't need the
// exception.
func skipModelSuffixForProvider(providerName string) bool {
	return providerName == "opencode"
}

// isApiKeyHelperExecutable mirrors bootstrap.isExecutableFile for the
// at-Boot revalidation path — same predicate, scoped here so the agent
// package doesn't import bootstrap (would close a cycle: bootstrap
// already imports agent).
func isApiKeyHelperExecutable(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !st.Mode().IsRegular() {
		return false
	}
	// At least one execute bit (owner / group / other).
	return st.Mode().Perm()&0o111 != 0
}
