package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
	"github.com/hollis-labs/agentkit/agentlaunch/providerplant"
	"github.com/hollis-labs/agentkit/agentlaunch/sessionshim"
	runtimebootdir "github.com/hollis-labs/agentkit/agentruntime/bootdir"
	"github.com/hollis-labs/agentkit/agentruntime/sessionkit"
	"github.com/hollis-labs/agentkit/agentruntime/turn"
	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-agent-wrapper/activity"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-agent-wrapper/wrapper"
	llmtypes "github.com/hollis-labs/go-llm-types"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-sandbox/sandbox"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/launchprofile"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"go.opentelemetry.io/otel/attribute"
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
func Boot(ctx context.Context, deps *Dependencies, opts Options) (sess *Session, err error) {
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

	// Resolve the launch profile (and the underlying config.AgentProfile) up
	// front. LaunchProfile is the first-class user-facing selector; when
	// empty, AgentProfile flows through the legacy-compat path inside the
	// resolver (which may map to a builtin family or pass through verbatim).
	resolved := launchprofile.Resolve(launchprofile.ResolveRequest{
		LaunchProfile:      opts.LaunchProfile,
		LegacyAgentProfile: opts.AgentProfile,
		Source:             deps.Profiles,
	})
	profile := resolved.AgentProfile
	agentProfileName := resolved.AgentProfileName

	// Runtime kind selection drives BOTH the adapter constructor AND the
	// capability set the lib reads to pick the session implementation
	// (subprocess / pty / streaming-stdio / jsonrpc-stdio). Operators
	// override per-profile via profile.RuntimeKind; the per-Boot escape
	// hatch is opts.RuntimeKindOverride. Computing the kind first keeps
	// the adapter wiring in lockstep with the runtime caps.
	runtimeKind, err := resolveRuntimeKind(profile, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: runtime kind: %v", ErrAdapterNotFound, err)
	}

	cliAdapter, caps, err := adapterFor(profile, agentProfileName, runtimeKind)
	if err != nil {
		return nil, fmt.Errorf("%w: adapter: %v", ErrAdapterNotFound, err)
	}

	// Session ID + role (used for SessionMeta + boot.md content). Role
	// precedence: explicit Options.Role > LaunchProfile.Role > legacy
	// agent_profile name (mirrors pre-refactor behavior).
	idFn := opts.IDFn
	if idFn == nil {
		idFn = mgr.idFn
	}
	sessID := idFn()
	role := opts.Role
	if role == "" {
		role = resolved.Profile.Role
	}
	if role == "" {
		role = agentProfileName
	}

	// Trace the boot. Spans start AFTER the cheap preflight (validate / deps
	// guard / profile / runtime / adapter / sessID) so failed early returns
	// don't generate empty boot spans for misconfiguration churn; the deferred
	// RecordError below picks up anything thrown from here onward (loopback
	// setup, providerplant, agentsessions.Start, the jsonrpc kickoff). Domain
	// attrs use the hollis.* portfolio namespace; torque-specific axes (Mode,
	// profile, role) use torque.*. boot_dir is set later once captured.
	ctx, span := feotel.StartSpan(ctx, "torque.agent.boot")
	span.SetAttributes(
		attribute.String("hollis.app", "torque"),
		attribute.String("hollis.agent.id", sessID),
		attribute.String("hollis.task.id", opts.TaskID),
		attribute.String("hollis.project.id", opts.ProjectID),
		attribute.String("hollis.provider", profile.Provider),
		attribute.String("hollis.runtime.kind", string(runtimeKind)),
		attribute.String("torque.agent.mode", opts.Mode.String()),
		attribute.String("torque.agent.profile", agentProfileName),
		attribute.String("torque.launch_profile", resolved.Profile.ID),
		attribute.String("torque.agent.role", role),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
		}
		span.End()
	}()

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

	// apiKeyHelper threading (CW-20260509-0016 / CW-20260513-0017):
	// thread Dependencies.ApiKeyHelperPath onto the adapter BEFORE
	// providerplant.Plant runs, because the .claude/settings.json
	// Render closure captures the receiver at render time and emits
	// `apiKeyHelper: <path>` only when the field is non-empty. The
	// planted .claude/settings.json sits in cwd=bootDir and is read by
	// ANY claude invocation (bare or streaming) — claude's cwd-local
	// config discovery applies in non-bare mode, and bare mode
	// explicitly references it via --settings.
	//
	// Post Stage-2 (CW-20260515-0020): boot dirs are planted by
	// go-agent-launch's providerplant.Plant, NOT the session lib's
	// AutoPlantBootDir. providerplant.Plant renders the adapter's
	// BootDirSpec.PlantedFiles directly against the live adapter, so the
	// ApiKeyHelperPath set here flows into the planted settings.json the
	// same way. apiKeyHelper is deliberately NOT routed through the
	// shared InjectionSpec — it is a secret-bearing, provider-adapter-
	// specific path and InjectionSpec is persisted at rest. Wired for
	// all ClaudeAdapter instances (Bare, PTY, StreamingStdio); the field
	// is inert when no planted settings.json is read by claude.
	if claudeAdapter, ok := cliAdapter.(*provider.ClaudeAdapter); ok && deps.ApiKeyHelperPath != "" {
		// Validate the resolved path is still an executable file at the
		// moment we're about to thread it into the adapter. Catches the
		// race where the helper was removed between resolveApiKeyHelperPath
		// at startup and the first dispatch (rare but recoverable). Invalid
		// path → log + skip the adapter set; claude falls back to
		// ANTHROPIC_API_KEY in env (existing CW-20260509-0011 contract) or
		// (non-bare modes) to the operator's ~/.claude.json keychain auth.
		// Failing fast vs. degrading gracefully: degrade. A missing helper
		// shouldn't block a dispatch on systems where another auth path
		// works fine — operators will see the misconfig in the log line.
		if isApiKeyHelperExecutable(deps.ApiKeyHelperPath) {
			claudeAdapter.ApiKeyHelperPath = deps.ApiKeyHelperPath
		} else {
			log.Printf("agent.Boot: deps.ApiKeyHelperPath=%q no longer points at an executable file; skipping (claude will rely on ANTHROPIC_API_KEY or ~/.claude.json keychain auth)", deps.ApiKeyHelperPath)
		}
	}

	kickoffMD := kickoffMarkdown(opts, role)
	loopbackURL := ""
	if loopback != nil {
		loopbackURL = loopback.URL()
	}

	// Workspace layout (the shared four-root model — see WorkspaceLayout).
	// WorkspaceCreate materializes the durable per-session state/logs tree
	// and populates RepoRoot, WorkRoot, WorkspaceDir, BuildDirRoot.
	//
	// RepoRoot/WorkRoot: WorkRoot is opts.Workdir — the per-launch writable
	// dir. The scheduler owns per-run worktree creation (worktree.Spec.Resolve)
	// and hands Boot the already-resolved WorkRoot via opts.Workdir; Boot does
	// not create worktrees of its own. RepoRoot is opts.RepoRoot — the
	// canonical checkout — falling back to opts.Workdir when unset (shared
	// mode, where work_root == repo_root). The scheduler sets opts.RepoRoot
	// to the real repo root alongside the worktree work_root so the two stay
	// distinct in worktree mode.
	//
	// BuildDirRoot: $TMPDIR/torque-boot — the parent dir go-agent-launch's
	// launcher.Prepare materializes per-run boot dirs under (threaded via
	// LaunchPlan.Workspace.TempPrefix). The launcher's basename pattern
	// (agentlaunch-bootdir-<planhash>-*) does NOT contain "torque-boot",
	// but the parent dir does — so cross-app forensic tooling
	// (`find /var/folders -path '*torque-boot*'`) still surfaces them.
	ws, err := WorkspaceCreate(deps.WorkspacesRoot, opts.ProjectID, sessID, resolveRepoRoot(opts), opts.Workdir)
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: workspace: %v", ErrBootFailed, err)
	}

	// Env: base (filtered OS + TORQUE_TASK_ID/RUN_ID + agent-file env +
	// opts.Env). Per-provider env amendments (e.g. OPENCODE_CONFIG_DIR =
	// <bootDir>, CODEX_HOME = <bootDir>) are merged in after planting from
	// prepared.Env (providerplant.Plant resolves the BootDirSpec env
	// amendments against the planted bootdir).
	env := composeEnv(profile, opts, agentFile)

	// Shared-launch preparation + boot-dir planting (CW-20260515-0020).
	//
	// Torque builds a go-agent-launch LaunchPlan from its own Boot inputs,
	// compiles + prepares it, threads the task-scoped PlantContext fields
	// (MCP loopback URL + the daemon-scoped mux MCP entry) onto the
	// PreparedLaunch, and plants the provider boot dir via
	// providerplant.Plant — all BEFORE the session starts.
	//
	// This replaces go-agent-sessions v0.9.4's AutoPlantBootDir: the
	// StartRequest below sets AutoPlantBootDir:false so the session layer
	// does not double-plant. The planted boot dir, the bootdir-derived
	// env amendments, and the project-dir argv flow into the existing
	// StartOptions; everything downstream (kickoff, teardown, one-shot
	// turn wait) is unchanged.
	// Map Torque's runtime-kind / provider-id enums onto the agentlaunch
	// taxonomy and compose the planted task-bundle native files. These are
	// agent-package internals (factory.go selects the adapter; task_context.go
	// composes the bundle), so they are resolved here rather than inside the
	// launchprofile package — the overlay carries the already-mapped values
	// into the shared assembly seam.
	rtKind, err := mapRuntimeKind(runtimeKind)
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: map runtime kind: %v", ErrBootFailed, err)
	}
	nativeFiles := taskContextNativeFiles(taskContextInput{
		Options:          opts,
		SessionID:        sessID,
		AgentProfileName: agentProfileName,
		Role:             role,
		LoopbackURL:      loopbackURL,
	})
	injection, _, err := runtimebootdir.BuildInjection(runtimebootdir.Request{
		Provider:    mapProviderID(profile.Provider),
		Runtime:     rtKind,
		NativeFiles: nativeFiles,
	})
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: build injection: %v", ErrBootFailed, err)
	}

	plan := launchprofile.BuildLaunchPlan(resolved, launchprofile.TaskLaunchOverlay{
		SessionID:      sessID,
		Role:           role,
		AgentFilePath:  opts.AgentFile,
		RuntimeKind:    rtKind,
		ProviderID:     mapProviderID(profile.Provider),
		ProjectID:      opts.ProjectID,
		Workdir:        opts.Workdir,
		WorkspaceDir:   ws.WorkspaceDir,
		BuildDirRoot:   ws.BuildDirRoot,
		SystemPrompt:   systemPrompt,
		KickoffMD:      kickoffMD,
		LoopbackURL:    loopbackURL,
		PermissionMode: resolveLaunchPermissionMode(profile),
		Injection:      injection,
	})

	compiled, err := launcher.Compile(ctx, plan)
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: compile launch: %v", ErrBootFailed, err)
	}
	// launcher.Prepare's allocateBootDir does os.MkdirTemp(TempPrefix, ...)
	// — which fails if the prefix dir is absent. The session lib's old
	// AutoPlantBootDir MkdirAll'd BootDirRoot for us; providerplant's
	// Prepare does not, so ensure $TMPDIR/torque-boot exists here.
	//
	// 0o700 (user-private), matching the workspace tree (WorkspaceCreate):
	// planted boot dirs hold sensitive files (.claude/settings.json with the
	// apiKeyHelper path, MCP loopback config) and live under a world-writable
	// $TMPDIR, so the root must not be group/other-traversable.
	if err := os.MkdirAll(ws.BuildDirRoot, 0o700); err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: ensure boot dir root: %v", ErrBootFailed, err)
	}
	prepared, err := launcher.Prepare(ctx, compiled)
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: prepare launch: %v", ErrBootFailed, err)
	}
	// Task-scoped + daemon-scoped PlantContext fields the shared plan
	// does not carry: the MCP loopback URL (Torque still CONSTRUCTS the
	// loopback itself — only the URL flows here) and the mux MCP entry
	// (deps.MuxCommand/MuxArgs/MuxEnv). These are runtime values, kept
	// off the persisted-at-rest LaunchPlan deliberately.
	prepared.PlantContext.MCPLoopbackURL = loopbackURL
	prepared.PlantContext.SelfMCPCommand = deps.MuxCommand
	prepared.PlantContext.SelfMCPArgs = append([]string(nil), deps.MuxArgs...)
	prepared.PlantContext.SelfMCPEnv = muxEnvSliceToMap(deps.MuxEnv)
	// Plant the provider boot dir. WithAdapter pins the exact adapter
	// Torque resolved (adapterFor) — critically the BARE-mode claude
	// adapter, which providerplant's DefaultResolver would not select
	// (it returns plain claude). Planting against the same adapter
	// instance Torque spawns keeps the planted files byte-identical to
	// the pre-Stage-2 AutoPlantBootDir output.
	// PrepareExecution (agentkit v0.6.1+, shared materialize.Engine
	// underneath) replaces the legacy Plant() call: Plant() ran the same
	// projection/materialization internally but copied only Argv/Env/
	// Workdir back onto the legacy PreparedLaunch, silently discarding the
	// computed Materialization handle, AccessRequirements, and
	// CapabilityDiagnostics (CW-20260906-0111). We replicate Plant()'s
	// copy-back manually (prepared.Argv/Env/Workdir) so the unchanged
	// sessionshim.ToSessionLaunch(prepared) call below keeps working, and
	// additionally surface the previously-discarded diagnostics. preparedExecution
	// is kept alive past this call site for CW-20260904-0098's wrapper.Config
	// to consume directly, avoiding a second plant.
	bootDirProvider, hasBootDir := cliAdapter.(provider.BootDirProvider)
	var preparedExecution *agentlaunch.PreparedExecution
	if hasBootDir {
		preparedExecution, err = providerplant.PrepareExecution(ctx, prepared, providerplant.WithAdapter(bootDirProvider))
		if err != nil {
			shutdownLoopbackHandle(loopback)
			return nil, fmt.Errorf("%w: plant boot dir: %v", ErrBootFailed, err)
		}
		if profile.Provider == "codex" {
			if err := prepareCodexAuth(ctx, env, preparedExecution); err != nil {
				shutdownLoopbackHandle(loopback)
				_ = os.RemoveAll(prepared.PlantedBootDir)
				return nil, fmt.Errorf("%w: prepare Codex authentication: %v", ErrBootFailed, err)
			}
		}
		// A newly planted directory has no interactive Claude trust grant.
		// Load the operator-selected settings explicitly so headless launches
		// honor the planted permission mode and auth helper from the outset.
		if profile.Provider == "claude-code" {
			preparedExecution.Bindings.Argv = append(preparedExecution.Bindings.Argv,
				"--settings", filepath.Join(prepared.PlantedBootDir, ".claude", "settings.json"))
		}
		prepared.Argv = append([]string(nil), preparedExecution.Bindings.Argv...)
		prepared.Env = envVarValues(preparedExecution.Bindings.Env)
		prepared.Workdir = preparedExecution.Bindings.CWD
		logPlantResult(sessID, preparedExecution)
	}
	sessionLaunch, err := sessionshim.ToSessionLaunch(prepared)
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: convert prepared launch: %v", ErrBootFailed, err)
	}
	// capturedBootDir is the planted dir; "" for adapters with no
	// BootDirSpec (gemini/copilot — unsupported in Torque today, but the
	// branch keeps Boot generic). Planting already happened, so this is
	// known up-front rather than via an OnBootDirPlanted callback.
	capturedBootDir := ""
	if hasBootDir {
		capturedBootDir = prepared.PlantedBootDir
	}

	pb := &plantedBoot{
		resolved:          resolved,
		profile:           profile,
		agentProfileName:  agentProfileName,
		runtimeKind:       runtimeKind,
		cliAdapter:        cliAdapter,
		caps:              caps,
		sessID:            sessID,
		role:              role,
		systemPrompt:      systemPrompt,
		kickoffMD:         kickoffMD,
		loopback:          loopback,
		loopbackURL:       loopbackURL,
		ws:                ws,
		env:               env,
		preparedExecution: preparedExecution,
		capturedBootDir:   capturedBootDir,
		sessionLaunch:     sessionLaunch,
	}

	// CW-20260904-0098: claude/opencode's runtime kinds (streaming-stdio,
	// subprocess, serve-http) adopt go-agent-wrapper for spawn/lifecycle/
	// events. Two kinds stay on Torque's existing agentsessions-direct
	// pipeline:
	//   - JsonRpcStdio (codex): wrapper.Wrapper exposes no equivalent to
	//     agentsessions.Manager.JsonRpcCall, which codex's multi-turn
	//     app-server protocol requires (Tesseract go_agent_wrapper_gaps_2026_09_07).
	//   - PTY: go-agent-wrapper's adapters.Select has no PTY LaunchMode for
	//     native (non-ACP) runtimes, and no shipped profile defaults to PTY
	//     today (runtime_kind.go: "operator escape hatch", unproven for the
	//     dogfooded providers) -- nothing exercises this path, so there is
	//     no reason to force a mapping go-agent-wrapper doesn't model.
	// deps.RuntimeFactory also forces the legacy path regardless of runtime
	// kind: wrapper.Wrapper.Run() has no analogous override hook, so
	// RuntimeFactory (Torque's existing fake-runtime test seam,
	// internal/e2e/agent_boot) can only be honored there -- the natural
	// generalization of "when a caller substitutes runtime construction,
	// use the path where that substitution applies."
	useWrapper := deps.RuntimeFactory == nil &&
		runtimeKind != RuntimeKindJsonRpcStdio &&
		runtimeKind != RuntimeKindPTY
	if useWrapper {
		return bootWrapper(ctx, deps, mgr, opts, pb)
	}
	return bootLegacy(ctx, deps, mgr, opts, pb)
}

// plantedBoot bundles everything Boot's shared prefix (profile resolution
// through boot-dir planting) computes, so bootLegacy and bootWrapper can
// consume it without re-deriving or re-planting.
type plantedBoot struct {
	resolved          launchprofile.CompiledLaunchProfile
	profile           config.AgentProfile
	agentProfileName  string
	runtimeKind       RuntimeKind
	cliAdapter        provider.CLIAdapter
	caps              agentsessions.Capabilities
	sessID            string
	role              string
	systemPrompt      string
	kickoffMD         string
	loopback          LoopbackHandle
	loopbackURL       string
	ws                *WorkspaceLayout
	env               []string
	preparedExecution *agentlaunch.PreparedExecution
	capturedBootDir   string
	sessionLaunch     sessionshim.SessionLaunch
}

// bootLegacy drives the pre-CW-20260904-0098 spawn/lifecycle path: direct
// agentsessions.NewFromAdapter construction, mgr.inner.Start, and (for
// JsonRpcStdio/codex) the SendTurn/JsonRpcCall turn-delivery machinery
// go-agent-wrapper has no equivalent for. Used for codex's runtime kind
// always, and for every runtime kind when deps.RuntimeFactory is set
// (Torque's fake-runtime test seam, which only this path can honor).
func bootLegacy(ctx context.Context, deps *Dependencies, mgr *Manager, opts Options, pb *plantedBoot) (sess *Session, err error) {
	resolved := pb.resolved
	profile := pb.profile
	agentProfileName := pb.agentProfileName
	runtimeKind := pb.runtimeKind
	cliAdapter := pb.cliAdapter
	caps := pb.caps
	sessID := pb.sessID
	systemPrompt := pb.systemPrompt
	kickoffMD := pb.kickoffMD
	loopback := pb.loopback
	ws := pb.ws
	env := pb.env
	capturedBootDir := pb.capturedBootDir
	sessionLaunch := pb.sessionLaunch

	// Permission mode (CW-20260517-0038 Variation 3) is threaded into the
	// claude-code adapter in adapterFor via ClaudeAdapter.PermissionMode,
	// so go-providers (v0.19.0+) plants permissions.defaultMode directly —
	// no post-Plant settings.json rewrite here.

	// Boot-dir leak guard (CW-20260515-0020 follow-up). Post Stage-2 the
	// boot dir is planted to disk HERE, before runtime construction,
	// runtime.Prepare, the ModeResume checkpoint lookup, encodeMeta, and
	// deps.CreateSession — each of which can return an error. The session
	// lib's AutoPlantBootDir used to own teardown (planting happened
	// inside Start); now planting is external + earlier, so every error
	// return between this point and a successful mgr.inner.Start() would
	// leak the planted temp dir under $TMPDIR/torque-boot.
	//
	// Single deferred cleanup guarded by bootDirPlanted: it fires for
	// every intermediate pre-Start failure and is a no-op once cleared.
	// The flag is cleared the instant mgr.inner.Start() returns nil, so
	// the success path keeps the dir (registerBootDir / OneShot inline
	// cleanup take over) and the Start-error path — which clears the flag
	// too — does its own os.RemoveAll without this defer double-removing.
	bootDirPlanted := capturedBootDir != ""
	defer func() {
		if bootDirPlanted {
			_ = os.RemoveAll(capturedBootDir)
		}
	}()

	// Thread the planted boot dir into the adapter / session argv.
	//
	// Two distinct mechanisms, by adapter shape:
	//
	//   - Bare-mode claude: the boot dir is referenced via four explicit
	//     CLI flags (--mcp-config / --append-system-prompt-file /
	//     --settings / --add-dir) that ClaudeAdapter.BuildArgs emits from
	//     the adapter's own MCPConfigPath/AppendSystemPromptFile/
	//     SettingsPath/ProjectDir fields. We populate them here from
	//     BareInjectionPaths(plantedBootDir, projectDir). Because BuildArgs
	//     already emits --add-dir for the bare adapter, the ExtraArgs
	//     splice below is suppressed for bare claude to avoid a double
	//     --add-dir.
	//
	//   - Non-bare adapters (claude PTY/streaming, codex subprocess,
	//     opencode): the boot dir is referenced via the BootDirSpec's
	//     ProjectDirArg (--add-dir / --cd / --dir) plus EnvAmendments
	//     (CODEX_HOME / OPENCODE_CONFIG_DIR). providerplant.Plant resolved
	//     both into prepared.Argv[1:] and prepared.Env; we thread Argv[1:]
	//     into StartOptions.ExtraArgs (a public field consumers may set
	//     directly — the runtime splices it after adapter.BuildArgs) and
	//     merge prepared.Env into the spawn env.
	var bootDirExtraArgs []string
	bootDirExtraArgs = append([]string(nil), sessionLaunch.Options.ExtraArgs...)

	// opencode serve-http hot-fix: `opencode serve` does NOT accept
	// `--dir <path>` (that's an `opencode run` flag) and exits printing
	// help-to-stderr when an unknown flag arrives, producing the
	// "process exited before printing listen URL" failure mode at the
	// go-agent-sessions serve-http startup gate. The providerplant
	// resolver currently emits OpencodeBootDirSpec.ProjectDirArg
	// ("--dir {{.ProjectDir}}") unconditionally regardless of runtime;
	// for serve-http mode the projectDir is already conveyed via
	// spawnWorkdir (cwd) + OPENCODE_CONFIG_DIR env, so dropping the
	// bootdir-derived argv splice is safe.
	//
	// Discovered 2026-05-21 during the PR #92 smoke test of profile
	// opencode-claude-long against opencode 1.15.6 (4 consecutive
	// runs failed ~300ms each before this fix).
	//
	// Substrate-side follow-up: providerplant.DefaultResolver should
	// suppress ProjectDirArg when plan.Runtime == RuntimeServeHTTP
	// (a go-agent-launch matrix or providerplant patch). When that
	// lands, this Torque-side branch becomes redundant and can be
	// removed.
	if shouldDropBootDirExtraArgs(profile.Provider, runtimeKind) {
		bootDirExtraArgs = nil
	}
	if profile.Provider == "codex" && runtimeKind == RuntimeKindJsonRpcStdio {
		// The JSON-RPC runtime already calls the adapter's BuildArgs, which
		// emits app-server. PreparedExecution contains that same command;
		// only its remaining options belong in the ExtraArgs splice.
		if len(bootDirExtraArgs) > 0 && bootDirExtraArgs[0] == "app-server" {
			bootDirExtraArgs = bootDirExtraArgs[1:]
		}
		// This runtime bypasses the per-turn buildArgs callback below.
		// app-server accepts model configuration via -c, not --model.
		bootDirExtraArgs = append(append([]string(nil), profile.Args...), bootDirExtraArgs...)
		if profile.Model != "" {
			bootDirExtraArgs = append(bootDirExtraArgs, "-c", fmt.Sprintf("model=%q", profile.Model))
		}
	}

	// Merge the bootdir-derived env amendments (CODEX_HOME /
	// OPENCODE_CONFIG_DIR) that providerplant.Plant resolved into
	// prepared.Env. Torque's composeEnv already produced the base
	// "K=V" slice; append the amendments last so they win.
	env = mergePreparedEnv(env, sessionLaunch.Options.Env)

	// Spawn cwd: providerplant.Plant set prepared.Workdir to the spec's
	// SpawnWorkdir (bootDir for claude/codex, projectDir for opencode).
	// Fall back to opts.Workdir when no boot dir was planted.
	spawnWorkdir := opts.Workdir
	if capturedBootDir != "" && sessionLaunch.Options.Workdir != "" {
		spawnWorkdir = sessionLaunch.Options.Workdir
	}

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
			Adapter:         cliAdapter,
			Profile:         profile,
			SystemPrompt:    systemPrompt,
			TurnPrompt:      turnPrompt,
			SessionID:       sessionID,
			SkipModelSuffix: skipModelSuffix,
		})
	}

	runtimeFactory := agentsessions.NewFromAdapter
	if deps.RuntimeFactory != nil {
		runtimeFactory = func(cfg agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			return deps.RuntimeFactory(cfg)
		}
	}
	runtime, err := runtimeFactory(agentsessions.AdapterRuntimeConfig{
		ID:        "torque-cli/" + cliAdapter.Name(),
		Kind:      string(runtimeKind),
		Adapter:   cliAdapter,
		Caps:      caps,
		BuildArgs: buildArgs,
		WaitDelay: cancelGraceFromEnv(),
	})
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: construct runtime: %v", ErrBootFailed, err)
	}
	if err := runtime.Prepare(ctx); err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: prepare runtime: %v", ErrBootFailed, err)
	}

	// Resume preset: ModeResume threads the provider's session ID via
	// SessionIDPreset. Lookup goes through the named checkpoint. The
	// state-based resume path (Manager.ResumeSession, sprint α.2) skips
	// the checkpoint lookup and threads the persisted-on-row session-id
	// directly via Options.ProviderSessionIDOverride; that path stays on
	// ModeLongLived but still feeds SessionIDPreset.
	var sessionIDPreset string
	var resumeHint []byte
	if opts.Mode == ModeResume {
		cp, err := findCheckpoint(deps.Store, opts.ResumeFromCheckpoint)
		if err != nil {
			shutdownLoopbackHandle(loopback)
			return nil, fmt.Errorf("%w: %v", ErrBootFailed, err)
		}
		if len(cp.ResumeHint) > 0 {
			sessionIDPreset = string(cp.ResumeHint)
			resumeHint = cp.ResumeHint
		}
	}
	if opts.ProviderSessionIDOverride != "" {
		// Explicit override wins over the checkpoint-derived preset above
		// — ResumeSession threads the canonical state-based preset here
		// and the caller picked the override deliberately. resumeHint is
		// updated in lockstep so the freshly-rebooted session row carries
		// the same provider session-id forward (otherwise the row's
		// resume_hint column would briefly diverge from SessionIDPreset
		// until the next OnSessionID callback fires).
		sessionIDPreset = opts.ProviderSessionIDOverride
		resumeHint = []byte(opts.ProviderSessionIDOverride)
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
	// metaKeyBootDir is populated post-Start once OnBootDirPlanted fires
	// (the lib's preparePlant runs synchronously inside Start, before the
	// child spawn, so the captured path is available immediately after
	// Start returns nil). Workdir on the row stays at opts.Workdir — the
	// project root, which is the most useful forensic value; the planted
	// bootDir lives in metaKeyBootDir and registerBootDir.
	persistedMeta := make(map[string]string, len(opts.SessionMeta)+3)
	for k, v := range opts.SessionMeta {
		persistedMeta[k] = v
	}
	persistedMeta[metaKeyMode] = opts.Mode.String()
	persistedMeta[metaKeyWorkspaceDir] = ws.WorkspaceDir
	if opts.ParentSessionID != "" {
		persistedMeta[metaKeyParentSessionID] = opts.ParentSessionID
	}
	metaJSON, err := encodeMeta(persistedMeta)
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: encode session meta: %v", ErrBootFailed, err)
	}
	rec := &sqlstore.SessionRecord{
		ID:            sessID,
		LaunchProfile: resolved.Profile.ID,
		AgentProfile:  agentProfileName,
		Provider:      profile.Provider,
		RuntimeID:     runtime.ID(),
		RuntimeKind:   runtime.Kind(),
		Workdir:       opts.Workdir,
		ProjectID:     nullableString(opts.ProjectID),
		TaskID:        nullableString(opts.TaskID),
		State:         string(StatusLaunching),
		ResumeHint:    resumeHint,
		MetaJSON:      metaJSON,
	}
	if err := deps.CreateSession(context.Background(), rec); err != nil {
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
	// drives SendInput / SendTurn synchronously and gets one turn's exit
	// code back. The flag flows uniformly through StartOptions for
	// ModeLongLived/Subagent/Background — substrate consumers (orchestrator
	// boot, subagent spawn, background fire-and-forget) get a single
	// declarative contract: "boot fires the first turn for you" — without
	// a Boot-side SendInput plumbing path that would race the Launch
	// (CW-20260507-0011's class of bug).
	//
	// JsonRpcStdio is the exception: the lib's AutoFireFirstTurn path
	// calls SendInput which on the JsonRpcStdio session is the raw-bytes
	// escape hatch (writes plaintext to stdin, no JSON-RPC framing). The
	// codex app-server rejects unframed input. For JsonRpcStdio long-lived
	// modes, AutoFireFirstTurn stays false and Boot drives the kickoff
	// post-Start via SendTurn (which runs the JSON-RPC handshake +
	// turn/start sequence). See the post-Start block below.
	autoFire := (opts.Mode == ModeLongLived ||
		opts.Mode == ModeSubagent ||
		opts.Mode == ModeBackground) && runtimeKind != RuntimeKindJsonRpcStdio
	var firstTurnPayload []byte
	if autoFire {
		firstTurnOpts := agentsessions.StartOptions{}
		turnRuntime, err := mapRuntimeKind(runtimeKind)
		if err != nil {
			return nil, fmt.Errorf("%w: map first-turn runtime: %v", ErrBootFailed, err)
		}
		if err := sessionkit.ApplyFirstTurnPolicy(&firstTurnOpts, sessionkit.FirstTurnPolicy{
			Mode:   sessionkit.AutoFireFirstTurn,
			Prompt: kickoffPayload(""),
			Turn: turn.Options{
				Provider: profile.Provider,
				Runtime:  turnRuntime,
			},
		}); err != nil {
			return nil, fmt.Errorf("%w: frame first turn: %v", ErrBootFailed, err)
		}
		firstTurnPayload = firstTurnOpts.FirstTurnPayload
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
	// ModeOneShot turn-complete signal. Wired into startStreamFanout's
	// onDone hook below so the ModeOneShot block at the end of Boot can
	// wait for the session's `done` event before issuing Stop + WaitSession
	// — long-lived adapters (StreamingStdio / app-server) stay alive past
	// the turn, so the prior hardcoded 5s grace SIGTERM'd them mid-turn
	// (followups_torque_streaming_oneshot_turn_complete_wait).
	// Subprocess-per-turn adapters also emit EventDone before exit, so the
	// signal is uniformly available across runtime kinds.
	//
	// Allocated unconditionally for ModeOneShot so the select below can
	// rely on a non-nil channel; remains uncreated for non-OneShot modes
	// (no consumer). PTY runtime leaves streamFanout nil — onDone never
	// fires there, the select falls through to ctx.Done(); not a regression
	// vs the prior 5s behavior, and post-Step-3 PTY isn't a target default.
	var oneshotDone chan struct{}
	var oneshotOnDone func()
	if opts.Mode == ModeOneShot {
		oneshotDone = make(chan struct{})
		var doneOnce sync.Once
		oneshotOnDone = func() { doneOnce.Do(func() { close(oneshotDone) }) }
	}

	const streamFanoutDepth = 64
	var streamFanout chan llmtypes.StreamEvent
	closeStreamFanout := func() {}
	if !caps.PTY {
		// last_activity gate: the drain calls Manager.observeStreamEvent for
		// every stream event so error / auth-dead frames freeze the PID
		// poller's heartbeat-driven TouchSession until a content-bearing
		// event arrives. teardownSession clears the gate entry on terminal
		// state. CW-20260519-0130.
		onActivityEvent := func(ev llmtypes.StreamEvent) {
			mgr.observeStreamEvent(sessID, ev)
		}
		streamFanout, closeStreamFanout = startStreamFanout(ws.LogDir, streamFanoutDepth, opts.eventFanout, oneshotOnDone, onActivityEvent)
	}

	// JSON-RPC notification hook. For codex JsonRpcStdio sessions, the
	// `turn/completed` notification (emitted by the codex app-server
	// after the turn finishes) is the turn-complete signal. Adapter
	// .ParseLine returns nil for codex app-server mode (per
	// go-providers v0.17.1 pty_codex.go) so streamFanout's EventDone
	// hook never fires for JsonRpcStdio — wiring the notification hook
	// is the only way ModeOneShot can detect turn-complete on this
	// runtime kind. sync.Once guard inside oneshotOnDone keeps both
	// routes idempotent: whichever signal arrives first wins, the
	// other is a no-op.
	var jsonRpcNotificationHook func(string, json.RawMessage)
	if runtimeKind == RuntimeKindJsonRpcStdio {
		hookOnDone := oneshotOnDone // nil for non-oneshot modes
		// emit projects a codex-derived StreamEvent into the stream fanout
		// (→ stream.jsonl histogram + downstream cost accumulator +
		// last_activity gate). Non-blocking: the jsonrpc reader goroutine
		// drives this hook and must never block on a full fanout buffer.
		emit := func(ev llmtypes.StreamEvent) {
			if streamFanout == nil {
				return
			}
			// forwardEventNonBlocking guards send-on-closed (defer recover)
			// AND is non-blocking: a late codex JSON-RPC notification can be
			// delivered by the reader goroutine after closeStreamFanout()
			// has run during teardown — a raw `select { case streamFanout
			// <- ev }` would panic the daemon on the closed channel. It also
			// must never block the single jsonrpc reader goroutine on a full
			// buffer. (Copilot PR #93.)
			forwardEventNonBlocking(streamFanout, ev)
		}
		// codex thread/tokenUsage totals are cumulative; track the last
		// seen totals so each update emits a per-update delta
		// (translateStreamEvent sums InputTokens/OutputTokens). Closure-
		// captured and only touched from the single jsonrpc reader
		// goroutine, so no lock is needed.
		var prevTok turn.CodexTokenUsageTotals
		jsonRpcNotificationHook = func(method string, params json.RawMessage) {
			switch method {
			case string(turn.CodexTurnCompleted):
				// codex's app-server emits slash-style JSON-RPC method names
				// (`thread/start`, `turn/start`, `turn/completed`) verbatim;
				// turn.CodexTurnCompleted is the single source of truth for the
				// wire string. A mismatch never fires turn-complete, so
				// ModeOneShot burns its full timeout budget then SIGTERMs a
				// turn that already succeeded.
				if hookOnDone != nil {
					hookOnDone()
				}
			case string(turn.CodexItemCompleted):
				// Tool calls (commandExecution/fileChange) + assistant
				// text → stream.jsonl histogram + transcript + liveness.
				// CW-20260521-0024.
				if ev, ok := codexItemCompletedEvent(params); ok {
					emit(ev)
				}
			case string(turn.CodexTokenUsageUpdated):
				// Cumulative totals → per-update delta → EventUsage so the
				// run record accrues codex's real token/cost (was $0/0).
				// turn.CodexTokenUsageDelta does the cumulative→delta math
				// with negative-clamping.
				totals, ok := turn.ParseCodexTokenUsageTotals(params)
				if !ok {
					return
				}
				d := turn.CodexTokenUsageDelta(prevTok, totals)
				prevTok = totals
				if d.InputTokens == 0 && d.OutputTokens == 0 && d.CachedInputTokens == 0 {
					return
				}
				emit(llmtypes.StreamEvent{
					Type: llmtypes.EventUsage,
					Usage: &llmtypes.Usage{
						InputTokens:     d.InputTokens,
						OutputTokens:    d.OutputTokens,
						CacheReadTokens: d.CachedInputTokens,
					},
				})
			}
		}
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

	// Boot dir is already planted by providerplant.Plant (above), so
	// StartOptions.AutoPlantBootDir is false — the session layer must
	// not double-plant. capturedBootDir was resolved at plant time;
	// ExtraArgs / Workdir / Env carry the planted layout into the spawn.
	//
	// BootPrompt / BootContent still flow so the existing kickoff path
	// (boot.md @-reference + AutoFireFirstTurn) is unchanged — the
	// session layer consumes them for the first-turn payload framing
	// independently of planting. PlantContext is left zero: nothing in
	// the session layer reads it when AutoPlantBootDir is false.
	startReq := agentsessions.StartRequest{
		ID:      sessID,
		Runtime: runtime,
		Options: agentsessions.StartOptions{
			Workdir:                 spawnWorkdir,
			WorkspaceDir:            ws.WorkspaceDir,
			LogPath:                 ws.LogPath,
			BootPrompt:              systemPrompt,
			BootContent:             kickoffMD,
			Env:                     env,
			Stderr:                  stderrWriter,
			Profile:                 sandboxProfile,
			AttachEnabled:           true,
			AutoFireFirstTurn:       autoFire,
			FirstTurnPayload:        firstTurnPayload,
			SessionIDPreset:         sessionIDPreset,
			OnSessionID:             onSessionID,
			Supervisor:              supervisor,
			ResourceLimits:          limits,
			EventFanout:             streamFanout,
			TypedEventCallback:      opts.TypedEventCallback,
			AutoPlantBootDir:        false,
			ExtraArgs:               bootDirExtraArgs,
			PlantContext:            sessionLaunch.Options.PlantContext,
			JsonRpcNotificationHook: jsonRpcNotificationHook,
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
		shutdownLoopbackHandle(loopback)
		// Inner.Start records StateFailed via StateSink on its own; no extra
		// row update needed here. Boot dir planting happened BEFORE Start
		// (providerplant.Plant), so the lib no longer owns its cleanup —
		// remove the planted dir here on the Start-failed path. Clear the
		// leak-guard flag first so the deferred cleanup does not double-remove.
		if capturedBootDir != "" {
			bootDirPlanted = false
			_ = os.RemoveAll(capturedBootDir)
		}
		return nil, fmt.Errorf("%w: %v", ErrBootFailed, err)
	}
	// Start succeeded — the planted boot dir is now owned by the running
	// session (registerBootDir below for long-lived modes, the OneShot
	// inline defer for ModeOneShot). Clear the leak guard so the deferred
	// cleanup is a no-op on every success path.
	bootDirPlanted = false

	// Persist the captured boot dir into SessionMeta. providerplant.Plant
	// resolved capturedBootDir before Start, so it is known here (or empty
	// when the adapter has no BootDirSpec — gemini/copilot today). Empty
	// path → skip the meta write; Get/List paths return Session.BootDir
	// = "" which matches the no-plant reality.
	if capturedBootDir != "" {
		persistedMeta[metaKeyBootDir] = capturedBootDir
		if updatedMeta, encErr := encodeMeta(persistedMeta); encErr == nil {
			if updErr := deps.UpdateSessionMeta(context.Background(), sessID, updatedMeta); updErr != nil {
				log.Printf("agent.Boot: persist bootDir into session meta failed (sessID=%s bootDir=%s): %v", sessID, capturedBootDir, updErr)
			}
		}
	}

	// Register per-session teardown hooks for non-OneShot modes. OneShot
	// runs synchronously below and drives its own teardown via Stop.
	// Boot dir cleanup is consumer-owned post Stage-2 (the session lib's
	// AutoPlantBootDir terminal-state cleanup is no longer in play):
	// registerBootDir hands the planted dir to Manager.teardownSession,
	// which os.RemoveAll's it on Stop / terminal-state observation.
	if opts.Mode != ModeOneShot {
		mgr.registerLoopback(sessID, loopback)
		mgr.registerStderrCloser(sessID, closeStderr)
		mgr.registerStreamCloser(sessID, closeStreamFanout)
		if capturedBootDir != "" {
			mgr.registerBootDir(sessID, capturedBootDir)
		}
		// Per-session PID poller (CW-20260509-0008): the lib records pid only
		// at launch (always 0 for adapter-mode) and never refreshes the row's
		// last_activity between turns. The poller bridges that gap and drives
		// a clean Stop the moment Health.Alive flips false so orchestrators
		// finish in state=done instead of waiting for the next daemon-restart
		// sweep to mark the row crashed.
		mgr.registerPidPoller(sessID, startPidPoller(mgr, sessID, mgr.pidPollInterval))
	}

	sess = &Session{
		ID:              sessID,
		Mode:            opts.Mode,
		LaunchProfile:   resolved.Profile.ID,
		AgentProfile:    agentProfileName,
		Provider:        profile.Provider,
		RuntimeID:       runtime.ID(),
		RuntimeKind:     runtime.Kind(),
		Workdir:         opts.Workdir,
		BootDir:         capturedBootDir,
		WorkspaceDir:    ws.WorkspaceDir,
		ProjectID:       opts.ProjectID,
		TaskID:          opts.TaskID,
		ParentSessionID: opts.ParentSessionID,
		Status:          StatusLaunching, // updated on terminal observe
		Meta:            opts.SessionMeta,
		CreatedAt:       time.Now().UTC(),
	}

	// Post-Start kickoff for long-lived JsonRpcStdio. The lib's
	// AutoFireFirstTurn path would call SendInput with plaintext on a
	// JSON-RPC session — the codex app-server rejects unframed input.
	// So for ModeLongLived/Subagent/Background on JsonRpcStdio runtimes,
	// AutoFireFirstTurn stayed false (above) and we fire the kickoff
	// here via SendTurn, which runs the JSON-RPC handshake
	// (initialize + thread/start) + turn/start sequence. Synchronous —
	// blocks until the RPC calls return. The session keeps running
	// after this returns; the first turn streams via notifications. If
	// SendTurn fails (binary missing, app-server rejected the call),
	// log + roll back: stop the session and mark the row failed.
	//
	// Resume paths skip the kickoff. ModeResume + ProviderSessionIDOverride
	// both want to re-thread an existing transcript, not fire a fresh
	// first turn; for JsonRpcStdio the proper resume sequence is
	// `thread/resume` (instead of `thread/start`) followed by
	// turn/start, which is a follow-up captured as
	// followups.torque.codex_jsonrpc_resume_wireup. For now
	// resume on JsonRpcStdio falls through to whatever the lib +
	// SessionIDPreset path negotiate (codex app-server ignores
	// SessionIDPreset; the session boots cold). Operators wanting
	// real codex resume should pin profile.RuntimeKind: subprocess
	// until the JSON-RPC resume wireup lands.
	isResume := opts.Mode == ModeResume || opts.ProviderSessionIDOverride != ""
	if opts.Mode != ModeOneShot && runtimeKind == RuntimeKindJsonRpcStdio && !isResume {
		kickoff := composeUserPrompt(opts)
		if kickoff == "" {
			kickoff = kickoffPayloadForBootDir(capturedBootDir)
		}
		if err := mgr.SendTurn(context.WithoutCancel(ctx), sess, kickoff); err != nil {
			log.Printf("agent.Boot: long-lived JsonRpcStdio kickoff failed (session=%s): %v", sessID, err)
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = mgr.inner.Stop(stopCtx, sessID)
			stopCancel()
			sess.Status = StatusFailed
			return sess, fmt.Errorf("%w: jsonrpc kickoff: %v", ErrBootFailed, err)
		}
	}

	// ModeOneShot drives the turn synchronously: SendTurn delivers the
	// kickoff, the select waits for turn-complete (oneshotDone fires
	// from the streamFanout EventDone hook OR the JsonRpcNotificationHook
	// on `turn/completed`) or ctx.Done() (the executor wraps ctx in
	// context.WithTimeout(profile.TimeoutSeconds) — see the comment on
	// the startCtx detachment branch above), then Stop tears down and
	// WaitSession surfaces the exit code.
	if opts.Mode == ModeOneShot {
		defer closeStderr()
		defer closeStreamFanout()
		defer shutdownLoopbackHandle(loopback)
		// BootDir cleanup is consumer-owned post Stage-2: providerplant
		// .Plant materialized the dir before Start, so the session lib
		// does not reap it. Remove it inline once the synchronous OneShot
		// turn + Stop have completed.
		if capturedBootDir != "" {
			defer func() { _ = os.RemoveAll(capturedBootDir) }()
		}

		prompt := composeUserPrompt(opts)
		if prompt == "" {
			if runtimeKind == RuntimeKindJsonRpcStdio {
				prompt = kickoffPayloadForBootDir(capturedBootDir)
			} else {
				prompt = kickoffPayload("")
			}
		}
		// SendTurn routes by runtime kind: JsonRpcStdio runs the
		// initialize+thread/start+turn/start handshake (with thread_id
		// cache on Manager); subprocess/streaming-stdio fall through to
		// the plaintext Manager.SendInput path. Either way, the
		// turn-complete signal arrives via oneshotDone (closed by the
		// streamFanout EventDone hook for subprocess/streaming, or by
		// the JsonRpcNotificationHook on `turn/completed` for
		// JsonRpcStdio).
		sendErr := mgr.SendTurn(ctx, sess, prompt)

		// Wait for turn-complete (oneshotDone fires from the streamFanout
		// onDone hook when EventDone passes through) OR the executor's
		// budgeted ctx firing. The prior implementation hardcoded a 5s
		// grace which SIGTERM'd long-lived adapters (StreamingStdio /
		// app-server) mid-turn; binding the wait to profile.TimeoutSeconds
		// via ctx aligns with feedback_torque_timeout_philosophy (no
		// new hardcoded timeouts; profile timeouts are the budget).
		// Subprocess-per-turn adapters emit EventDone before exit, so the
		// wait collapses to the prior fast-path for short turns.
		var timedOut bool
		if sendErr == nil && oneshotDone != nil {
			select {
			case <-oneshotDone:
				// Turn-complete observed.
			case <-ctx.Done():
				timedOut = true
				log.Printf("agent.Boot: ModeOneShot session=%s timed out before turn_complete (ctx.Err=%v)", sessID, ctx.Err())
			}
		}

		// Stop + WaitSession with a short grace independent of the
		// turn-budget ctx (which is already done in the timeout branch).
		// Background context bounds only the termination handshake, not
		// the turn itself — the lib's runner sends SIGTERM (with its own
		// internal grace) and reaps the child; both finish sub-second on
		// the happy path. Keep the 5s ceiling as a safety net for
		// pathological subprocess hangs during teardown.
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
		case timedOut:
			sess.Status = StatusFailed
		case exitCode != 0:
			sess.Status = StatusFailed
		default:
			sess.Status = StatusDone
		}
	}

	return sess, nil
}

// bootWrapper drives the CW-20260904-0098 go-agent-wrapper-routed spawn/
// lifecycle path for claude/opencode's runtime kinds (streaming-stdio,
// subprocess, serve-http). Planting already happened in Boot's shared prefix
// (providerplant.PrepareExecution, CW-20260906-0111) -- wrapper.Config.
// PreparedExecution carries that result directly, so wr.Run() never
// re-plants (its internal runPlanter is skipped whenever
// PreparedExecution.Materialization is already set).
func bootWrapper(ctx context.Context, deps *Dependencies, mgr *Manager, opts Options, pb *plantedBoot) (sess *Session, err error) {
	profile := pb.profile
	agentProfileName := pb.agentProfileName
	runtimeKind := pb.runtimeKind
	cliAdapter := pb.cliAdapter
	caps := pb.caps
	sessID := pb.sessID
	loopback := pb.loopback
	ws := pb.ws
	env := pb.env
	preparedExecution := pb.preparedExecution
	capturedBootDir := pb.capturedBootDir

	if preparedExecution == nil {
		// No BootDirSpec (gemini/copilot) -- unsupported in Torque today;
		// adapterFor never resolves these providers, so this should be
		// unreachable. Fail closed rather than risk a nil-deref below.
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: go-agent-wrapper path requires a planted boot dir", ErrBootFailed)
	}

	// Boot-dir leak guard, same contract as the legacy path (CW-20260515-0020
	// follow-up): planting already happened before this function runs, so
	// every error return before the wrapper session is admitted would
	// otherwise leak the planted dir.
	bootDirPlanted := capturedBootDir != ""
	defer func() {
		if bootDirPlanted {
			_ = os.RemoveAll(capturedBootDir)
		}
	}()

	// Neutralize the derived sandbox policy (go_agent_wrapper_gaps_2026_09_07
	// Finding 3): providerplant's defaultAccessRequirements sets
	// Mode: AccessRequired with the project root READ-ONLY (write is only
	// granted on the state root) -- agentsessions derives a real go-sandbox
	// ResolvedAccessPolicy from any AccessRequired PreparedExecution and
	// enforces it at spawn. Torque runs with zero sandbox enforcement today
	// (opts.SandboxProfile below is the only confinement mechanism); a
	// session whose entire job is editing the project directly must not
	// have that silently downgraded to read-only. AccessOptional
	// short-circuits agentsessions' sandboxPolicyFromPrepared to a no-op
	// while leaving the Argv/Env/CWD bindings -- the actual reason to feed
	// PreparedExecution through -- untouched.
	execution := *preparedExecution
	execution.Access.Mode = agentlaunch.AccessOptional

	// opencode serve-http hot-fix (factory.go's shouldDropBootDirExtraArgs,
	// ported from the legacy path): `opencode serve` rejects `--dir <path>`
	// (an `opencode run`-only flag); providerplant's OpencodeBootDirSpec
	// emits it unconditionally. agentsessions.applyStartOptions derives
	// StartOptions.ExtraArgs from Bindings.Argv[1:] unconditionally once
	// PreparedExecution is set, so the trim has to happen on the bindings
	// themselves rather than on a separate ExtraArgs override.
	if shouldDropBootDirExtraArgs(profile.Provider, runtimeKind) && len(execution.Bindings.Argv) > 0 {
		execution.Bindings.Argv = execution.Bindings.Argv[:1]
	}
	// PreparedExecution is the wrapper's complete spawn command; it suppresses
	// CLIAdapter.BuildArgs. Carry Claude's profile options on that command,
	// rather than an adapter callback that the prepared path never invokes.
	if profile.Provider == "claude-code" && len(execution.Bindings.Argv) > 0 {
		argv := []string{execution.Bindings.Argv[0]}
		argv = append(argv, profileArgsExcludingDevFlag(profile)...)
		argv = append(argv, execution.Bindings.Argv[1:]...)
		if profile.Model != "" {
			argv = append(argv, "--model", profile.Model)
		}
		execution.Bindings.Argv = argv
	}

	// Merge Torque's own composeEnv output (TORQUE_TASK_ID/RUN_ID, filtered
	// OS env, agent-file env, opts.Env) into the bindings env --
	// providerplant only resolved the provider-side amendments (CODEX_HOME /
	// OPENCODE_CONFIG_DIR); agentsessions derives the FINAL spawn env
	// entirely from Bindings.Env once PreparedExecution is set (same
	// override behavior as ExtraArgs above), so Torque's base vars have to
	// land there too. Provider-derived keys win on collision, matching the
	// legacy path's mergePreparedEnv(env, sessionLaunch.Options.Env) precedence.
	execution.Bindings.Env = mergeCallerEnvIntoPrepared(env, execution.Bindings.Env)

	spawnWorkdir := opts.Workdir
	if capturedBootDir != "" && execution.Bindings.CWD != "" {
		spawnWorkdir = execution.Bindings.CWD
	}

	launchMode, err := adaptersLaunchModeFor(runtimeKind)
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: %v", ErrBootFailed, err)
	}
	// adapters.Select wraps Torque's already-configured cliAdapter (bare-mode
	// claude, dev-mode variants, permission mode, apiKeyHelper -- adapterFor
	// is still authoritative for all of it) into the shape wrapper.Config.
	// Adapter needs, without reimplementing per-provider adapter construction.
	wrapperAdapter, err := adapters.Select(adapters.Selection{
		Provider:   adaptersProviderFor(profile.Provider),
		LaunchMode: launchMode,
		CLIAdapter: cliAdapter,
	})
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: select wrapper adapter: %v", ErrBootFailed, err)
	}

	// Resume preset: same contract as the legacy path (boot.go's own
	// ModeResume / ProviderSessionIDOverride handling).
	var sessionIDPreset string
	var resumeHint []byte
	if opts.Mode == ModeResume {
		cp, cpErr := findCheckpoint(deps.Store, opts.ResumeFromCheckpoint)
		if cpErr != nil {
			shutdownLoopbackHandle(loopback)
			return nil, fmt.Errorf("%w: %v", ErrBootFailed, cpErr)
		}
		if len(cp.ResumeHint) > 0 {
			sessionIDPreset = string(cp.ResumeHint)
			resumeHint = cp.ResumeHint
		}
	}
	if opts.ProviderSessionIDOverride != "" {
		sessionIDPreset = opts.ProviderSessionIDOverride
		resumeHint = []byte(opts.ProviderSessionIDOverride)
	}

	var onSessionID func(string)
	if caps.ProviderSessionID && deps.Store != nil {
		onSessionID = func(id string) {
			_ = deps.UpdateSessionResumeHint(context.Background(), sessID, []byte(id))
		}
	}

	persistedMeta := make(map[string]string, len(opts.SessionMeta)+3)
	for k, v := range opts.SessionMeta {
		persistedMeta[k] = v
	}
	persistedMeta[metaKeyMode] = opts.Mode.String()
	persistedMeta[metaKeyWorkspaceDir] = ws.WorkspaceDir
	if opts.ParentSessionID != "" {
		persistedMeta[metaKeyParentSessionID] = opts.ParentSessionID
	}
	metaJSON, err := encodeMeta(persistedMeta)
	if err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: encode session meta: %v", ErrBootFailed, err)
	}
	rec := &sqlstore.SessionRecord{
		ID:            sessID,
		LaunchProfile: pb.resolved.Profile.ID,
		AgentProfile:  agentProfileName,
		Provider:      profile.Provider,
		RuntimeID:     "torque-cli/" + cliAdapter.Name(),
		RuntimeKind:   string(runtimeKind),
		Workdir:       opts.Workdir,
		ProjectID:     nullableString(opts.ProjectID),
		TaskID:        nullableString(opts.TaskID),
		State:         string(StatusLaunching),
		ResumeHint:    resumeHint,
		MetaJSON:      metaJSON,
	}
	if err := deps.CreateSession(context.Background(), rec); err != nil {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: create session row: %v", ErrBootFailed, err)
	}

	sandboxProfile := sandbox.Profile{}
	if opts.SandboxProfile != nil {
		sandboxProfile = *opts.SandboxProfile
		sandboxProfile.AllowLoopback = true
	}

	// AutoFireFirstTurn: same contract as the legacy path's non-JsonRpcStdio
	// branch (this function never handles JsonRpcStdio -- codex stays on
	// bootLegacy).
	autoFire := opts.Mode == ModeLongLived || opts.Mode == ModeSubagent || opts.Mode == ModeBackground
	var firstTurnPayload []byte
	if autoFire {
		firstTurnOpts := agentsessions.StartOptions{}
		turnRuntime, mapErr := mapRuntimeKind(runtimeKind)
		if mapErr != nil {
			shutdownLoopbackHandle(loopback)
			return nil, fmt.Errorf("%w: map first-turn runtime: %v", ErrBootFailed, mapErr)
		}
		if err := sessionkit.ApplyFirstTurnPolicy(&firstTurnOpts, sessionkit.FirstTurnPolicy{
			Mode:   sessionkit.AutoFireFirstTurn,
			Prompt: kickoffPayload(""),
			Turn: turn.Options{
				Provider: profile.Provider,
				Runtime:  turnRuntime,
			},
		}); err != nil {
			shutdownLoopbackHandle(loopback)
			return nil, fmt.Errorf("%w: frame first turn: %v", ErrBootFailed, err)
		}
		firstTurnPayload = firstTurnOpts.FirstTurnPayload
	}

	stderrWriter, _, closeStderr := openStderrSidecar(opts.RunID, ws.LogPath)
	sidecar := openStreamSidecar(ws.LogDir)

	var oneshotDone chan struct{}
	var oneshotOnDone func()
	if opts.Mode == ModeOneShot {
		oneshotDone = make(chan struct{})
		var doneOnce sync.Once
		oneshotOnDone = func() { doneOnce.Do(func() { close(oneshotDone) }) }
	}

	readyCh := make(chan struct{})
	var readyOnce sync.Once
	sink := &torqueRuntimeEventSink{
		sessID:  sessID,
		mgr:     mgr,
		deps:    deps,
		sidecar: sidecar,
		fanout:  opts.eventFanout,
		stderr:  stderrWriter,
		onReady: func() { readyOnce.Do(func() { close(readyCh) }) },
		onDone:  oneshotOnDone,
	}

	wr, err := wrapper.New(wrapper.Config{
		App:               "torque",
		Adapter:           wrapperAdapter,
		Activity:          activity.NewBridge(sink),
		Workdir:           spawnWorkdir,
		SessionID:         sessID,
		PreparedExecution: &execution,
		SandboxProfile:    sandboxProfile,
		WorkspaceDir:      ws.WorkspaceDir,
		LogPath:           ws.LogPath,
		SessionIDPreset:   sessionIDPreset,
		OnSessionID:       onSessionID,
		AutoFireFirstTurn: autoFire,
		FirstTurnPayload:  string(firstTurnPayload),
		HeartbeatInterval: mgr.pidPollInterval,
	})
	if err != nil {
		closeStderr()
		sidecar.Close()
		shutdownLoopbackHandle(loopback)
		bootDirPlanted = false
		_ = os.RemoveAll(capturedBootDir)
		return nil, fmt.Errorf("%w: construct wrapper: %v", ErrBootFailed, err)
	}

	// Context detachment for non-OneShot modes, matching the legacy path's
	// rationale exactly (boot.go's startCtx comment): long-lived sessions
	// must outlive the caller's request-scoped ctx.
	runCtx := ctx
	if opts.Mode != ModeOneShot {
		runCtx = context.WithoutCancel(ctx)
	}
	runCtx, runCancel := context.WithCancel(runCtx)

	h := &wrapperHandle{wr: wr, runDone: make(chan struct{})}
	go func() {
		defer close(h.runDone)
		defer runCancel()
		h.runErr = wr.Run(runCtx)
		state := string(StatusDone)
		if h.runErr != nil {
			state = string(StatusFailed)
		}
		_ = deps.UpdateSessionState(context.Background(), sessID, state, 0, nil)
	}()

	// Block until KindSessionReady (SendInput/Stop become safe -- mirrors
	// mgr.inner.Start() returning nil on the legacy path) or wr.Run exiting
	// first (immediate spawn failure).
	select {
	case <-readyCh:
		_ = deps.UpdateSessionState(context.Background(), sessID, string(StatusRunning), 0, nil)
	case <-h.runDone:
		runCancel()
		closeStderr()
		sidecar.Close()
		shutdownLoopbackHandle(loopback)
		bootDirPlanted = false
		_ = os.RemoveAll(capturedBootDir)
		failErr := h.runErr
		if failErr == nil {
			failErr = fmt.Errorf("wrapper.Run exited before session became ready")
		}
		_ = deps.UpdateSessionState(context.Background(), sessID, string(StatusFailed), 0, nil)
		return nil, fmt.Errorf("%w: %v", ErrBootFailed, failErr)
	case <-ctx.Done():
		runCancel()
		<-h.runDone
		closeStderr()
		sidecar.Close()
		shutdownLoopbackHandle(loopback)
		bootDirPlanted = false
		_ = os.RemoveAll(capturedBootDir)
		_ = deps.UpdateSessionState(context.Background(), sessID, string(StatusFailed), 0, nil)
		return nil, fmt.Errorf("%w: %v", ErrBootFailed, ctx.Err())
	}
	bootDirPlanted = false

	if capturedBootDir != "" {
		persistedMeta[metaKeyBootDir] = capturedBootDir
		if updatedMeta, encErr := encodeMeta(persistedMeta); encErr == nil {
			if updErr := deps.UpdateSessionMeta(context.Background(), sessID, updatedMeta); updErr != nil {
				log.Printf("agent.Boot: persist bootDir into session meta failed (sessID=%s bootDir=%s): %v", sessID, capturedBootDir, updErr)
			}
		}
	}

	mgr.registerWrapperSession(sessID, h)
	if opts.Mode != ModeOneShot {
		mgr.registerLoopback(sessID, loopback)
		mgr.registerStderrCloser(sessID, closeStderr)
		mgr.registerStreamCloser(sessID, sidecar.Close)
		if capturedBootDir != "" {
			mgr.registerBootDir(sessID, capturedBootDir)
		}
		// No registerPidPoller here: KindSessionHeartbeat/KindProcessStarted
		// from the sink replace pid_poller.go's job for wrapper-routed
		// sessions (see wrapper_sink.go's Write doc comments).
	}

	sess = &Session{
		ID:              sessID,
		Mode:            opts.Mode,
		LaunchProfile:   pb.resolved.Profile.ID,
		AgentProfile:    agentProfileName,
		Provider:        profile.Provider,
		RuntimeID:       "torque-cli/" + cliAdapter.Name(),
		RuntimeKind:     string(runtimeKind),
		Workdir:         opts.Workdir,
		BootDir:         capturedBootDir,
		WorkspaceDir:    ws.WorkspaceDir,
		ProjectID:       opts.ProjectID,
		TaskID:          opts.TaskID,
		ParentSessionID: opts.ParentSessionID,
		Status:          StatusRunning,
		Meta:            opts.SessionMeta,
		CreatedAt:       time.Now().UTC(),
	}

	if opts.Mode == ModeOneShot {
		defer closeStderr()
		defer sidecar.Close()
		defer shutdownLoopbackHandle(loopback)
		if capturedBootDir != "" {
			defer func() { _ = os.RemoveAll(capturedBootDir) }()
		}

		prompt := composeUserPrompt(opts)
		if prompt == "" {
			prompt = kickoffPayload("")
		}
		// SendTurn (not raw SendInput) so streaming-stdio's turn.Frame NDJSON
		// encoding is applied -- claude rejects unframed plaintext on stdin
		// when running --input-format stream-json.
		sendErr := mgr.SendTurn(ctx, sess, prompt)

		var timedOut bool
		if sendErr == nil && oneshotDone != nil {
			select {
			case <-oneshotDone:
			case <-ctx.Done():
				timedOut = true
				log.Printf("agent.Boot: ModeOneShot session=%s timed out before turn_complete (ctx.Err=%v)", sessID, ctx.Err())
			}
		}

		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr.Stop(stopCtx, sessID)
		stopCancel()

		waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		exitCode, _ := mgr.Wait(waitCtx, sessID)
		waitCancel()

		sess.ExitCode = &exitCode
		switch {
		case sendErr != nil:
			sess.Status = StatusFailed
		case timedOut:
			sess.Status = StatusFailed
		case exitCode != 0:
			sess.Status = StatusFailed
		default:
			sess.Status = StatusDone
		}
	}

	return sess, nil
}

// mergeCallerEnvIntoPrepared merges Torque's caller-side "K=V" env slice
// into a PreparedExecution's Bindings.Env map, preserving whatever
// providerplant already resolved on collision -- matches the legacy path's
// mergePreparedEnv(callerEnv, providerAmendments) precedence, where
// provider-derived keys win.
func mergeCallerEnvIntoPrepared(callerEnv []string, existing map[string]agentlaunch.EnvVar) map[string]agentlaunch.EnvVar {
	out := make(map[string]agentlaunch.EnvVar, len(existing)+len(callerEnv))
	for k, v := range existing {
		out[k] = v
	}
	for _, kv := range callerEnv {
		key, val, found := strings.Cut(kv, "=")
		if !found || key == "" {
			continue
		}
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = agentlaunch.EnvVar{Value: val, Source: "caller", Precedence: 10}
	}
	return out
}

// adaptersProviderFor maps Torque's config.AgentProfile.Provider string onto
// go-agent-wrapper's adapters.Provider vocabulary. "claude-code" (Torque's
// public profile provider name) maps to go-providers' ClaudeAdapter.Name()
// == "claude", which is also adapters.ProviderClaude's wire value --
// adapters.Select validates the two agree.
func adaptersProviderFor(providerName string) adapters.Provider {
	switch providerName {
	case "claude-code":
		return adapters.ProviderClaude
	case "codex":
		return adapters.ProviderCodex
	case "opencode":
		return adapters.ProviderOpenCode
	default:
		return adapters.Provider(providerName)
	}
}

// adaptersLaunchModeFor maps a bootWrapper-reachable RuntimeKind onto
// go-agent-wrapper's adapters.LaunchMode. JsonRpcStdio and PTY never reach
// this function (Boot's dispatch routes them to bootLegacy).
func adaptersLaunchModeFor(kind RuntimeKind) (adapters.LaunchMode, error) {
	switch kind {
	case RuntimeKindStreamingStdio:
		return adapters.LaunchStreamingStdio, nil
	case RuntimeKindSubprocess:
		return adapters.LaunchSubprocessPerTurn, nil
	case RuntimeKindServeHTTP:
		return adapters.LaunchServeHTTP, nil
	default:
		return "", fmt.Errorf("no go-agent-wrapper launch mode for runtime kind %q", kind)
	}
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
// out of the buildArgs closure in Boot so the per-provider --model
// placement switch (SkipModelSuffix) can be unit-tested directly.
type buildArgsParams struct {
	Adapter         provider.CLIAdapter
	Profile         config.AgentProfile
	SystemPrompt    string
	TurnPrompt      string
	SessionID       string
	SkipModelSuffix bool
}

// composeBuildArgs assembles the per-turn argv. It calls the adapter's
// BuildArgs for the provider-shape baseline, optionally appends the
// generic --model suffix, and prepends profile.Args (minus the dev-mode
// flag, which is consumed by adapterFor → NewClaudeAdapterDev*).
//
// Project-directory args (--add-dir for claude, --cd for codex, --dir
// for opencode) are owned by go-agent-sessions v0.9.4's AutoPlantBootDir:
// the lib appends BootDirSpec.ProjectDirArg via planted.ExtraArgs and
// the runtime splices it into argv after BuildArgs; bare-mode claude
// adapters get the path threaded through a per-session adapter clone
// via applyBareInjection (lib drops the ExtraArgs splice in that
// branch to prevent double-emit).
//
// Per-provider exception:
//
//   - skipModelSuffix=true: opencode's argv requires --model BEFORE
//     the positional prompt, which OpencodeAdapter.BuildArgs already
//     emits when adapter.Model is set (factory.go threads it). The
//     trailing-suffix path would either land --model AFTER the
//     prompt (argv corruption) or duplicate the flag.
func composeBuildArgs(p buildArgsParams) []string {
	args := p.Adapter.BuildArgs(p.TurnPrompt, p.SystemPrompt, p.SessionID)
	if !p.SkipModelSuffix && p.Profile.Model != "" {
		args = append(args, "--model", p.Profile.Model)
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

// resolveRepoRoot returns the canonical project checkout (repo_root) for a
// Boot call: Options.RepoRoot when the caller supplied it (the scheduler does,
// alongside the per-run worktree work_root), falling back to Options.Workdir
// otherwise. The fallback preserves shared-mode behaviour where work_root ==
// repo_root, and keeps WorkspaceLayout.RepoRoot honest in worktree mode where
// Workdir points at the worktree rather than the canonical checkout.
func resolveRepoRoot(opts Options) string {
	if opts.RepoRoot != "" {
		return opts.RepoRoot
	}
	return opts.Workdir
}

// envVarValues flattens a PreparedExecution's map[string]agentlaunch.EnvVar
// into the map[string]string shape PreparedLaunch.Env uses (mirrors
// providerplant's private envVarMap, which Torque cannot import directly).
func envVarValues(in map[string]agentlaunch.EnvVar) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v.Value
	}
	return out
}

// logPlantResult surfaces the PreparedExecution fields the legacy Plant()
// call silently discarded: the materialize.Engine's reconcile report and
// any provider-capability diagnostics computed during planting. Previously
// nothing observed conflicts or unsupported-capability warnings from this
// step (CW-20260906-0111).
func logPlantResult(sessID string, execution *agentlaunch.PreparedExecution) {
	if execution == nil {
		return
	}
	if execution.Materialization != nil {
		report := execution.Materialization.Report
		log.Printf("agent.Boot: plant materialize session=%s operation=%s complete=%t changes=%d", sessID, report.Operation, report.Complete, len(report.Changes))
	}
	for _, d := range execution.Diagnostics {
		log.Printf("agent.Boot: plant diagnostic session=%s code=%s severity=%s outcome=%s feature=%s provider=%s: %s", sessID, d.Code, d.Severity, d.Outcome, d.Feature, d.Provider, d.Message)
	}
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
