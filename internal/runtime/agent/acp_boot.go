package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/hollis-labs/go-agent-wrapper/activity"
	"github.com/hollis-labs/go-agent-wrapper/launch"
	"github.com/hollis-labs/go-agent-wrapper/wrapper"
	"github.com/hollis-labs/go-providers/registry"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// ACP launch (CW-20261001-0097). An Agent Client Protocol session has no
// go-providers adapter and no boot dir: the agent takes its MCP servers in
// session/new rather than from a planted config, and its task bundle and
// kickoff arrive as the first turn. go-agent-wrapper owns the session end to
// end (initialize, session/new, session/prompt, cancel, close), so bootACP
// hands it the inputs Boot's shared prefix computed and skips planting.

// The MCP server names go-providers plants for native runtimes (codex's
// [mcp_servers.loopback], claude's and opencode's "loopback" and "mux"
// entries). An ACP session gets the same names, so a worker sees one tool
// namespace whichever way it was launched.
const (
	acpLoopbackMCPServer = "loopback"
	acpMuxMCPServer      = "mux"
)

// acpTaskDispatchRefusals names the ACP runtimes a scheduler-dispatched task
// run cannot use at all, and why. A task worker reports through the per-task
// loopback MCP (comments, review, blocked); one that cannot reach it would
// run until its timeout without signalling anything. Manual sessions are not
// refused: an operator driving one reads its output directly. pi-acp is
// listed because it passes no session MCP server to Pi, stdio included, so
// no capability Pi reports could change the answer.
var acpTaskDispatchRefusals = map[runtimes.ID]string{
	runtimes.Pi: "pi-acp cannot reach Torque's loopback MCP (mcpCapabilities.http=false); a task worker could not comment or signal review",
}

// acpTaskDispatchRefusal returns why a scheduler-dispatched task run cannot
// use the provider in the given runtime kind, or "" when it can. Other ACP
// agents are judged at launch, from what they report (bootACP).
func acpTaskDispatchRefusal(providerName string, kind RuntimeKind) string {
	if !kind.ACP() {
		return ""
	}
	desc, ok := registry.Lookup(providerName)
	if !ok {
		return ""
	}
	return acpTaskDispatchRefusals[desc.ID]
}

// acpLoopbackDroppedRefusal is why a long-lived task run stops at launch when
// the agent turned the loopback away. A long-lived worker ends by signalling
// review through the loopback; without it the run sits until its ceiling. A
// one-shot run ends with its turn, so it goes ahead.
func acpLoopbackDroppedRefusal(providerName string) string {
	return providerName + " does not accept HTTP MCP servers (no mcpCapabilities.http), so the loopback was not sent and a long-lived task worker could not signal review; one-shot runs and manual sessions are allowed"
}

// selectACPRuntime builds the wrapper's ACP adapter for (runtime, mode).
// launch.Select takes no go-providers adapter for an ACP mode; the profile's
// args (less Claude's native developer flag, which has no ACP meaning) are
// appended to the ACP agent's command.
func selectACPRuntime(profile config.AgentProfile, desc registry.Descriptor, mode runtimes.Mode) (selectedRuntime, error) {
	launched, err := launch.Select(launch.Selection{
		Runtime:   string(desc.ID),
		Mode:      mode,
		ExtraArgs: profileArgsExcludingDevFlag(profile),
	})
	if err != nil {
		return selectedRuntime{}, fmt.Errorf("%s provider, runtime kind %q: %w", profile.Provider, string(mode), err)
	}
	return selectedRuntime{wrapper: launched, caps: capabilitiesForRuntimeKind(RuntimeKind(mode))}, nil
}

// acpMCPServers is the MCP server set an ACP session receives in session/new
// (and session/load): the per-task loopback over streamable HTTP and, when
// withMux is set and the daemon has one, the mux aggregator over stdio.
// withMux is plantsMux's answer, which for ACP is bypassPermissions only. The
// values are the ones Boot hands go-providers for a native boot dir
// (PlantContext.MCPLoopbackURL and SelfMCPCommand/Args/Env). go-agent-wrapper
// sends the HTTP one only to an agent that advertises mcpCapabilities.http
// and reports the rest through OnACPDiagnostic (acpSkippedMCPServers).
func acpMCPServers(loopbackURL string, deps *Dependencies, withMux bool) []acp.MCPServer {
	var servers []acp.MCPServer
	if loopbackURL != "" {
		servers = append(servers, acp.MCPServer{Name: acpLoopbackMCPServer, URL: loopbackURL})
	}
	if withMux && deps != nil && deps.MuxCommand != "" {
		servers = append(servers, acp.MCPServer{
			Name:    acpMuxMCPServer,
			Command: deps.MuxCommand,
			Args:    append([]string(nil), deps.MuxArgs...),
			Env:     muxEnvSliceToMap(deps.MuxEnv),
		})
	}
	return servers
}

// acpSkippedMCPServers returns the servers an ACP diagnostic says were not
// sent to the agent, or nil. go-agent-wrapper reports them as one protocol
// diagnostic ending "not sent: <name>, <name>"
// (acp.ReportSkippedMCPServers); it is the only place Torque learns that an
// agent lacks mcpCapabilities.http.
func acpSkippedMCPServers(d acp.Diagnostic) []string {
	if d.Kind != acp.DiagnosticProtocol {
		return nil
	}
	_, names, ok := strings.Cut(d.Message, "not sent: ")
	if !ok || names == "" {
		return nil
	}
	return strings.Split(names, ", ")
}

// acpDiagnostics writes an ACP session's diagnostics (the agent's stderr,
// malformed frames, protocol notes such as dropped MCP servers) to the
// session log, and remembers whether the loopback was dropped. The wrapper
// bounds and redacts them before they get here.
type acpDiagnostics struct {
	mu              sync.Mutex
	w               io.Writer
	loopbackDropped atomic.Bool
}

func (d *acpDiagnostics) observe(diag acp.Diagnostic) {
	if slices.Contains(acpSkippedMCPServers(diag), acpLoopbackMCPServer) {
		d.loopbackDropped.Store(true)
	}
	line := fmt.Sprintf("acp %s: %s", diag.Kind, diag.Message)
	if diag.Raw != "" {
		line += ": " + diag.Raw
	}
	_, _ = d.Write([]byte(strings.TrimRight(line, "\n") + "\n"))
}

// Write serializes the session log's two writers: these diagnostics and the
// event sink's stderr lines.
func (d *acpDiagnostics) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.w.Write(p)
}

// acpKickoff is an ACP session's first turn. It is the native kickoff with
// the task bundle inline, since there is no boot dir to plant it in, and it
// names the loopback's tools only when the session has them.
func acpKickoff(opts Options, role string, bundle []agentlaunch.NativeFile, loopbackTools bool) string {
	body := kickoffHeader(opts, role)
	if loopbackTools {
		body += kickoffLoopbackTools
	} else {
		body += "Torque's task-scoped MCP tools are not available in this session. Report your result, or what blocks you, in your final message.\n\n"
	}
	var sections []string
	for _, f := range bundle {
		if f.RelPath == taskBundleReadmePath || !strings.HasSuffix(f.RelPath, ".md") {
			continue
		}
		sections = append(sections, strings.TrimRight(f.Content, "\n")+"\n")
	}
	if len(sections) > 0 {
		body += "Your assigned task bundle follows. This session has no boot dir, so where it mentions `task.md`, `task.json` or `process.md`, read the matching section here instead of a file.\n\n"
		body += strings.Join(sections, "\n") + "\n"
	}
	return body + kickoffFirstTurn(opts)
}

// bootACP launches an ACP session through go-agent-wrapper. It follows
// bootWrapper's lifecycle (session row, event sink, Run goroutine, ready
// wait, registration, ModeOneShot's inline turn) without a prepared
// execution: the wrapper resolves the ACP agent's command from its adapter,
// and the child gets Torque's composed environment as given.
func bootACP(ctx context.Context, deps *Dependencies, mgr *Manager, opts Options, pb *plantedBoot) (sess *Session, err error) {
	profile := pb.profile
	runtimeKind := pb.runtimeKind
	sessID := pb.sessID
	loopback := pb.loopback
	ws := pb.ws

	// A task worker that cannot reach the loopback cannot report; refuse
	// the run rather than let it time out. A boot that carries a RunID was
	// dispatched by the scheduler; manual sessions carry none.
	if opts.RunID > 0 {
		if reason := acpTaskDispatchRefusal(profile.Provider, runtimeKind); reason != "" {
			shutdownLoopbackHandle(loopback)
			return nil, fmt.Errorf("%w: %s", ErrAdapterNotFound, reason)
		}
	}
	if opts.SandboxProfile != nil && opts.SandboxProfile.ID != "" {
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: %s sessions take no go-sandbox profile (go-agent-wrapper refuses one for ACP agents)", ErrBootFailed, runtimeKind)
	}
	withMux := plantsMux(profile, runtimeKind)
	if !withMux && deps.MuxCommand != "" {
		log.Printf("agent.Boot: session=%s: mux MCP not offered to the ACP session for permission_mode %q; only bypassPermissions gets it (CW-20261001-0120)", sessID, profile.PermissionMode)
	}
	rawStderr, _, closeRawStderr := openStderrSidecar(opts.RunID, ws.LogPath)
	stderrWriter, closeStderr := redactStderr(rawStderr, closeRawStderr, pb.redact)
	diagnostics := &acpDiagnostics{w: stderrWriter}
	cfg := wrapper.Config{
		App:     "torque",
		Adapter: pb.wrapperAdapter,
		Workdir: opts.Workdir,
		// composeEnv already filtered the OS environment and added Torque's
		// own variables; the ACP agent gets exactly that.
		Environment:       wrapper.ChildEnvironment{Mode: wrapper.EnvironmentReplace, Set: pb.env},
		SessionID:         sessID,
		SystemPrompt:      pb.systemPrompt,
		WorkspaceDir:      ws.WorkspaceDir,
		LogPath:           ws.LogPath,
		HeartbeatInterval: mgr.pidPollInterval,
		// The control-plane directories the agent must not write
		// (CW-20261001-0141). go-agent-wrapper v0.25.0+ applies them to an ACP
		// launch with no resolved sandbox policy by running the child under
		// its protect-only profile (the host filesystem, writable, with these
		// directories read-only), and refuses the launch where the platform
		// cannot write-protect, so an ACP agent is protected like any other
		// (CW-20261001-0162). TORQUE_SANDBOX_PROTECT=0 leaves pb.protectedPaths
		// empty.
		ProtectedPaths:  pb.protectedPaths,
		ACPMCPServers:   acpMCPServers(pb.loopbackURL, deps, withMux),
		OnACPDiagnostic: diagnostics.observe,
		// The agent's permission requests are answered from the profile's
		// posture, each decision written to the session log
		// (acp_permissions.go).
		ACPBestEffortPermissionRequestResponder: acpPermissionResponder(permissionPosture(profile), diagnostics),
	}

	// Resume: ACP's session/load takes the provider session id, through the
	// wrapper's SessionIDPreset; same sources as the other paths.
	var resumeHint []byte
	if opts.Mode == ModeResume {
		cp, cpErr := findCheckpoint(deps.Store, opts.ResumeFromCheckpoint)
		if cpErr != nil {
			closeStderr()
			shutdownLoopbackHandle(loopback)
			return nil, fmt.Errorf("%w: %v", ErrBootFailed, cpErr)
		}
		if len(cp.ResumeHint) > 0 {
			cfg.SessionIDPreset = string(cp.ResumeHint)
			resumeHint = cp.ResumeHint
		}
	}
	if opts.ProviderSessionIDOverride != "" {
		cfg.SessionIDPreset = opts.ProviderSessionIDOverride
		resumeHint = []byte(opts.ProviderSessionIDOverride)
	}
	if pb.caps.ProviderSessionID && deps.Store != nil {
		cfg.OnSessionID = func(id string) {
			_ = deps.UpdateSessionResumeHint(context.Background(), sessID, []byte(id))
		}
	}

	persistedMeta := callerSessionMeta(opts.SessionMeta)
	persistedMeta[metaKeyMode] = opts.Mode.String()
	if cfg.SessionIDPreset != "" {
		// The launch continues a stored provider conversation
		// (Session.Resumed, CW-20261001-0203).
		persistedMeta[metaKeyResumed] = "true"
	}
	persistedMeta[metaKeyWorkspaceDir] = ws.WorkspaceDir
	if opts.RunID > 0 {
		persistedMeta[metaKeyRunID] = strconv.FormatInt(opts.RunID, 10)
	}
	if opts.ParentSessionID != "" {
		persistedMeta[metaKeyParentSessionID] = opts.ParentSessionID
	}
	metaJSON, err := encodeMeta(persistedMeta)
	if err != nil {
		closeStderr()
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: encode session meta: %v", ErrBootFailed, err)
	}
	runtimeID := "torque-cli/" + pb.wrapperAdapter.Name()
	rec := &sqlstore.SessionRecord{
		ID:            sessID,
		LaunchProfile: pb.resolved.Profile.ID,
		AgentProfile:  pb.agentProfileName,
		Provider:      profile.Provider,
		RuntimeID:     runtimeID,
		RuntimeKind:   string(runtimeKind),
		Workdir:       opts.Workdir,
		ProjectID:     nullableString(opts.ProjectID),
		TaskID:        nullableString(opts.TaskID),
		State:         string(StatusLaunching),
		ResumeHint:    resumeHint,
		MetaJSON:      metaJSON,
	}
	if err := deps.CreateSession(context.Background(), rec); err != nil {
		closeStderr()
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: create session row: %v", ErrBootFailed, err)
	}

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
	cfg.Activity = activity.NewBridge(&torqueRuntimeEventSink{
		sessID:  sessID,
		mgr:     mgr,
		deps:    deps,
		sidecar: sidecar,
		fanout:  opts.eventFanout,
		stderr:  diagnostics,
		redact:  pb.redact,
		onReady: func() { readyOnce.Do(func() { close(readyCh) }) },
		onDone:  oneshotOnDone,

		terminalFailure: opts.terminalFailure,
	})

	wr, err := wrapper.New(cfg)
	if err != nil {
		closeStderr()
		sidecar.Close()
		shutdownLoopbackHandle(loopback)
		_ = deps.UpdateSessionState(context.Background(), sessID, string(StatusFailed), 0, nil)
		return nil, fmt.Errorf("%w: construct wrapper: %v", ErrBootFailed, err)
	}

	// Long-lived sessions outlive the caller's request-scoped ctx, as on
	// the other paths.
	runCtx := ctx
	if opts.Mode != ModeOneShot && !opts.RetainContextOnLongLivedStart {
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
		mgr.endWrapperState(context.Background(), deps, sessID, state)
		mgr.finishWrapperSession(sessID, h)
	}()

	// session.ready arrives once session/new (or session/load) returns.
	select {
	case <-readyCh:
		_ = deps.UpdateSessionState(context.Background(), sessID, string(StatusRunning), 0, nil)
	case <-h.runDone:
		runCancel()
		closeStderr()
		sidecar.Close()
		shutdownLoopbackHandle(loopback)
		failErr := h.runErr
		if failErr == nil {
			failErr = fmt.Errorf("wrapper.Run exited before the ACP session became ready")
		}
		_ = deps.UpdateSessionState(context.Background(), sessID, string(StatusFailed), 0, nil)
		return nil, fmt.Errorf("%w: %v", ErrBootFailed, failErr)
	case <-ctx.Done():
		runCancel()
		<-h.runDone
		closeStderr()
		sidecar.Close()
		shutdownLoopbackHandle(loopback)
		_ = deps.UpdateSessionState(context.Background(), sessID, string(StatusFailed), 0, nil)
		return nil, fmt.Errorf("%w: %v", ErrBootFailed, ctx.Err())
	}

	// The agent's MCP capability is known now: the wrapper sends HTTP
	// servers only to an agent that advertises mcpCapabilities.http, and
	// reported any it dropped before session/new.
	loopbackDropped := diagnostics.loopbackDropped.Load()
	if opts.RunID > 0 && opts.Mode != ModeOneShot && loopbackDropped {
		_ = wr.Stop(context.Background())
		<-h.runDone
		closeStderr()
		sidecar.Close()
		shutdownLoopbackHandle(loopback)
		_ = deps.UpdateSessionState(context.Background(), sessID, string(StatusFailed), 0, nil)
		return nil, fmt.Errorf("%w: %s", ErrAdapterNotFound, acpLoopbackDroppedRefusal(profile.Provider))
	}

	mgr.registerWrapperSession(sessID, h)

	sess = &Session{
		ID:              sessID,
		Mode:            opts.Mode,
		LaunchProfile:   pb.resolved.Profile.ID,
		AgentProfile:    pb.agentProfileName,
		Provider:        profile.Provider,
		RuntimeID:       runtimeID,
		RuntimeKind:     string(runtimeKind),
		Workdir:         opts.Workdir,
		WorkspaceDir:    ws.WorkspaceDir,
		ProjectID:       opts.ProjectID,
		TaskID:          opts.TaskID,
		ParentSessionID: opts.ParentSessionID,
		Status:          StatusRunning,
		Resumed:         cfg.SessionIDPreset != "",
		Meta:            persistedMeta,
		CreatedAt:       time.Now().UTC(),
	}

	// The kickoff goes once the session exists, rather than as the
	// wrapper's auto-fired first turn, so it can say whether the loopback's
	// tools are there. ModeOneShot sends it below as its single turn.
	bundle := taskContextNativeFiles(taskContextInput{
		Options:          opts,
		SessionID:        sessID,
		AgentProfileName: pb.agentProfileName,
		Role:             pb.role,
		LoopbackURL:      pb.loopbackURL,
	})
	firstTurn := acpKickoff(opts, pb.role, bundle, pb.loopbackURL != "" && !loopbackDropped)

	if opts.Mode != ModeOneShot {
		if err := mgr.SendTurn(ctx, sess, firstTurn); err != nil {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = mgr.Stop(stopCtx, sessID)
			stopCancel()
			<-h.runDone
			closeStderr()
			sidecar.Close()
			shutdownLoopbackHandle(loopback)
			_ = deps.UpdateSessionState(context.Background(), sessID, string(StatusFailed), 0, nil)
			return nil, fmt.Errorf("%w: send ACP kickoff: %v", ErrBootFailed, err)
		}
		mgr.adoptWrapperResources(sessID, h, sessionResources{
			loopback:    loopback,
			closeStderr: closeStderr,
			closeStream: sidecar.Close,
		})
	}

	if opts.Mode == ModeOneShot {
		defer closeStderr()
		defer sidecar.Close()
		defer shutdownLoopbackHandle(loopback)

		sendErr := mgr.SendTurn(ctx, sess, firstTurn)

		var timedOut bool
		if sendErr == nil {
			select {
			case <-oneshotDone:
			case <-ctx.Done():
				timedOut = true
				log.Printf("agent.Boot: ModeOneShot ACP session=%s timed out before turn_complete (ctx.Err=%v)", sessID, ctx.Err())
			}
		}

		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr.Stop(stopCtx, sessID)
		stopCancel()

		waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		exitCode, _ := h.wait(waitCtx)
		waitCancel()

		sess.ExitCode = &exitCode
		switch {
		case sendErr != nil, timedOut, exitCode != 0:
			sess.Status = StatusFailed
		default:
			sess.Status = StatusDone
		}
	}

	return sess, nil
}
