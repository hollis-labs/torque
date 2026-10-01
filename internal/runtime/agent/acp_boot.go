package agent

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/go-agent-wrapper/activity"
	"github.com/hollis-labs/go-agent-wrapper/launch"
	"github.com/hollis-labs/go-agent-wrapper/wrapper"
	"github.com/hollis-labs/go-providers/provider"
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
// run cannot use, and why. A task worker reports through the per-task
// loopback MCP (comments, review, blocked); one that cannot reach it would
// run until its timeout without signalling anything. Manual sessions are not
// refused: an operator driving one reads its output directly.
var acpTaskDispatchRefusals = map[runtimes.ID]string{
	runtimes.Pi: "pi-acp cannot reach Torque's loopback MCP (mcpCapabilities.http=false); a task worker could not comment or signal review",
}

// acpTaskDispatchRefusal returns why a scheduler-dispatched task run cannot
// use the provider in the given runtime kind, or "" when it can.
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

// acpMCPServers is the MCP server set an ACP session should receive in
// session/new: the per-task loopback over streamable HTTP and, when the
// daemon has one, the mux aggregator over stdio. The values are the ones
// Boot hands go-providers for a native boot dir (PlantContext.MCPLoopbackURL
// and SelfMCPCommand/Args/Env), in go-providers' MCPServerSpec shape.
func acpMCPServers(loopbackURL string, deps *Dependencies) []provider.MCPServerSpec {
	var servers []provider.MCPServerSpec
	if loopbackURL != "" {
		servers = append(servers, provider.MCPServerSpec{Name: acpLoopbackMCPServer, HTTPURL: loopbackURL})
	}
	if deps != nil && deps.MuxCommand != "" {
		servers = append(servers, provider.MCPServerSpec{
			Name:    acpMuxMCPServer,
			Command: deps.MuxCommand,
			Args:    append([]string(nil), deps.MuxArgs...),
			Env:     append([]string(nil), deps.MuxEnv...),
		})
	}
	return servers
}

// attachACPMCPServers hands servers to the wrapper for session/new and
// reports whether they reach the agent. go-agent-wrapper v0.15.0 has no
// field for them: every ACP client it ships sends `"mcpServers": []`. Until
// the wrapper takes a server list, this returns false and the session runs
// without Torque's MCP tools.
func attachACPMCPServers(_ *wrapper.Config, _ []provider.MCPServerSpec) bool {
	return false
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
	}
	servers := acpMCPServers(pb.loopbackURL, deps)
	delivered := attachACPMCPServers(&cfg, servers)
	loopbackTools := delivered && pb.loopbackURL != ""
	if !delivered && len(servers) > 0 {
		log.Printf("agent.Boot: ACP session %s: %d MCP server(s) not delivered; go-agent-wrapper sends session/new an empty mcpServers (CW-20261001-0097)", sessID, len(servers))
	}
	bundle := taskContextNativeFiles(taskContextInput{
		Options:          opts,
		SessionID:        sessID,
		AgentProfileName: pb.agentProfileName,
		Role:             pb.role,
		LoopbackURL:      pb.loopbackURL,
	})
	firstTurn := acpKickoff(opts, pb.role, bundle, loopbackTools)

	// Resume: ACP's session/load takes the provider session id, through the
	// wrapper's SessionIDPreset; same sources as the other paths.
	var resumeHint []byte
	if opts.Mode == ModeResume {
		cp, cpErr := findCheckpoint(deps.Store, opts.ResumeFromCheckpoint)
		if cpErr != nil {
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
	persistedMeta[metaKeyWorkspaceDir] = ws.WorkspaceDir
	if opts.RunID > 0 {
		persistedMeta[metaKeyRunID] = strconv.FormatInt(opts.RunID, 10)
	}
	if opts.ParentSessionID != "" {
		persistedMeta[metaKeyParentSessionID] = opts.ParentSessionID
	}
	metaJSON, err := encodeMeta(persistedMeta)
	if err != nil {
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
		shutdownLoopbackHandle(loopback)
		return nil, fmt.Errorf("%w: create session row: %v", ErrBootFailed, err)
	}

	// The non-OneShot modes fire the kickoff as soon as session/new
	// returns; ModeOneShot sends it below as its single turn.
	if opts.Mode == ModeLongLived || opts.Mode == ModeSubagent || opts.Mode == ModeBackground {
		cfg.AutoFireFirstTurn = true
		cfg.FirstTurnPayload = firstTurn
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
	cfg.Activity = activity.NewBridge(&torqueRuntimeEventSink{
		sessID:  sessID,
		mgr:     mgr,
		deps:    deps,
		sidecar: sidecar,
		fanout:  opts.eventFanout,
		stderr:  stderrWriter,
		onReady: func() { readyOnce.Do(func() { close(readyCh) }) },
		onDone:  oneshotOnDone,
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
		_ = deps.UpdateSessionState(context.Background(), sessID, state, 0, nil)
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

	mgr.registerWrapperSession(sessID, h)
	if opts.Mode != ModeOneShot {
		mgr.registerLoopback(sessID, loopback)
		mgr.registerStderrCloser(sessID, closeStderr)
		mgr.registerStreamCloser(sessID, sidecar.Close)
	}

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
		Meta:            persistedMeta,
		CreatedAt:       time.Now().UTC(),
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
