package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
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

	// Adapter + capability set. Caps.PTY is decided per-Mode + provider.
	cliAdapter, baseCaps, err := adapterFor(profile, opts.AgentProfile)
	if err != nil {
		return nil, fmt.Errorf("%w: adapter: %v", ErrAdapterNotFound, err)
	}
	caps := baseCaps
	caps.PTY = shouldUsePTY(opts.Mode, profile.Provider, opts.SubprocessPerTurnOverride)

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

	// MCP loopback (closure-bound to taskID). nil disables (test path).
	loopback, err := setupLoopback(deps.Service, opts.TaskID)
	if err != nil {
		return nil, fmt.Errorf("%w: setup loopback: %v", ErrBootFailed, err)
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
	})
	if err != nil {
		shutdownLoopback(loopback)
		return nil, fmt.Errorf("%w: plant boot dir: %v", ErrBootFailed, err)
	}

	// Workspace dir (persistent state + logs). Independent of boot dir.
	ws, err := workspaceCreate(deps.WorkspacesRoot, opts.ProjectID, sessID)
	if err != nil {
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopback(loopback)
		return nil, fmt.Errorf("%w: workspace: %v", ErrBootFailed, err)
	}

	// Env: base (filtered OS + CLOCKWORK_TASK_ID/RUN_ID + agent-file env +
	// opts.Env) + per-provider amendments (e.g. OPENCODE_CONFIG_DIR).
	env := composeEnv(profile, opts, agentFile)
	env = append(env, layout.EnvAmendments...)

	// BuildArgs wrapper: prepends profile.Args (minus dev-mode flag, which
	// is consumed by adapterFor → NewClaudeAdapterDev), appends --model when
	// set, appends the lib's ProjectDirArg (e.g. --add-dir <projectDir>).
	buildArgs := func(turnPrompt, sessionID string) []string {
		args := cliAdapter.BuildArgs(turnPrompt, systemPrompt, sessionID)
		if profile.Model != "" {
			args = append(args, "--model", profile.Model)
		}
		if len(layout.ProjectDirArg) > 0 {
			args = append(args, layout.ProjectDirArg...)
		}
		if filtered := profileArgsExcludingDevFlag(profile); len(filtered) > 0 {
			args = append(filtered, args...)
		}
		return args
	}

	runtime, err := agentsessions.NewFromAdapter(agentsessions.AdapterRuntimeConfig{
		ID:        "clockwork-cli/" + cliAdapter.Name(),
		Kind:      "cli",
		Adapter:   cliAdapter,
		Caps:      caps,
		BuildArgs: buildArgs,
		WaitDelay: cancelGraceFromEnv(),
	})
	if err != nil {
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopback(loopback)
		return nil, fmt.Errorf("%w: NewFromAdapter: %v", ErrBootFailed, err)
	}
	if err := runtime.Prepare(ctx); err != nil {
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopback(loopback)
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
			shutdownLoopback(loopback)
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
			_ = deps.Store.UpdateSessionResumeHint(sessID, []byte(id))
		}
	}

	// Persist DB row in `launching` state. The lib's StateSink transitions
	// it through running → done/failed asynchronously via the watch
	// goroutine.
	metaJSON, err := encodeMeta(opts.SessionMeta)
	if err != nil {
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopback(loopback)
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
	if err := deps.Store.CreateSession(rec); err != nil {
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopback(loopback)
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
	// drives SendInput synchronously and gets one turn's exit code back.
	autoFire := opts.Mode == ModeLongLived ||
		opts.Mode == ModeSubagent ||
		opts.Mode == ModeBackground
	var firstTurnPayload []byte
	if autoFire {
		firstTurnPayload = []byte(kickoffPayload(layout.KickoffFile))
	}

	// Stderr sidecar: forward to per-run sidecar log + tail buffer (matches
	// CW-20260417-0024). PTY runtime merges stderr into the tty stream at
	// the kernel level, so the sidecar only fires on the adapter path.
	var stderrWriter io.Writer = io.Discard
	closeStderr := func() {}
	if !caps.PTY {
		w, _, closer := openStderrSidecar(opts.RunID)
		stderrWriter = w
		closeStderr = closer
	}

	// PTY-path Supervisor + ResourceLimits — go-agent-sessions v0.6.0 wires
	// these natively on the PTY runtime. Adapter-path forwarding is blocked
	// on go-runner publishing a v0.3.x with the supervision API exported
	// (the v0.3.0 tag points at a pre-supervision commit). For now we only
	// populate the fields when caps.PTY=true; the lib silently ignores them
	// on the adapter path.
	supervisor, limits := profileSupervision(profile, opts, caps.PTY)

	startReq := agentsessions.StartRequest{
		ID:      sessID,
		Runtime: runtime,
		Options: agentsessions.StartOptions{
			Workdir:           layout.SpawnCwd,
			WorkspaceDir:      ws.Root,
			LogPath:           ws.LogPath,
			BootPrompt:        systemPrompt,
			Env:               env,
			Stderr:            stderrWriter,
			Profile:           sandboxProfile,
			AttachEnabled:     true,
			AutoFireFirstTurn: autoFire,
			FirstTurnPayload:  firstTurnPayload,
			SessionIDPreset:   sessionIDPreset,
			OnSessionID:       onSessionID,
			Supervisor:        supervisor,
			ResourceLimits:    limits,
			EventFanout:       opts.eventFanout,
		},
		SessionMeta: opts.SessionMeta,
	}

	if err := mgr.inner.Start(ctx, startReq); err != nil {
		closeStderr()
		_ = os.RemoveAll(layout.BootDir)
		shutdownLoopback(loopback)
		// Inner.Start records StateFailed via StateSink on its own; no extra
		// row update needed here.
		return nil, fmt.Errorf("%w: %v", ErrBootFailed, err)
	}

	// Register per-session teardown hooks for non-OneShot modes. OneShot
	// runs synchronously below and drives its own teardown via Stop.
	if opts.Mode != ModeOneShot {
		mgr.registerLoopback(sessID, loopback)
		mgr.registerStderrCloser(sessID, closeStderr)
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
		defer shutdownLoopback(loopback)
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

// shutdownLoopback closes a loopback handle within a bounded deadline. Safe
// on nil handles (no-op).
func shutdownLoopback(h *loopbackHandle) {
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = h.Shutdown(ctx)
}

// findCheckpoint locates a checkpoint by ID across all sessions. The
// implementer prompt's pseudocode used a single-call helper; the underlying
// store's ListSessionCheckpoints is keyed by session ID, so we look up the
// checkpoint via a 1:N scan unless the caller can supply both IDs. For V1
// we accept that limitation and document it.
func findCheckpoint(store *sqlstore.Store, checkpointID string) (*sqlstore.SessionCheckpointRecord, error) {
	if store == nil {
		return nil, fmt.Errorf("nil store")
	}
	if checkpointID == "" {
		return nil, ErrNoCheckpoint
	}
	// The checkpoint ID encodes the source session indirectly; we walk
	// sessions newest-first and look for a matching ID. Bounded by the
	// session list page size; realistic boot times keep this cheap.
	sessions, err := store.ListSessions(sqlstore.SessionFilter{Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("list sessions for checkpoint lookup: %w", err)
	}
	for _, s := range sessions {
		cps, err := store.ListSessionCheckpoints(s.ID, 0)
		if err != nil {
			continue
		}
		for _, cp := range cps {
			if cp.ID == checkpointID {
				return cp, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: id=%s", ErrNoCheckpoint, checkpointID)
}

// profileSupervision derives Supervisor + ResourceLimits from the profile
// + opts. Returns nil/nil when the profile doesn't request supervision OR
// when the runtime is on the adapter path (where the v0.6.0 lib doesn't
// consume them yet).
//
// Profile shape today doesn't have explicit Supervisor / ResourceLimits
// fields — those are a follow-up to widen the AgentProfile struct. For
// V1 we honor profile.TimeoutSeconds → SupervisorOptions.IdleKill on PTY
// to give long-lived sessions a default ghost-kill window. Callers that
// want explicit values can subclass via opts.SessionMeta until the typed
// fields land.
func profileSupervision(profile config.AgentProfile, opts Options, ptyEnabled bool) (*agentsessions.SupervisorOptions, *agentsessions.ResourceLimits) {
	if !ptyEnabled {
		// v0.6.0 lib note: adapter-path forwarding is blocked on
		// go-runner v0.3.x publishing supervision; this branch returns
		// nil/nil to avoid stamping fields the lib will silently ignore.
		return nil, nil
	}
	// Honor only opts-supplied values for V1. Profile-config widening is
	// a follow-up. Today this returns nil/nil unless a future caller
	// stamps SessionMeta hints we'd interpret here — explicit no-op so
	// the wiring is in place for incremental expansion.
	_ = profile
	_ = opts
	return nil, nil
}

