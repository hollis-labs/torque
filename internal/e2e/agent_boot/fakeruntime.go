package agent_boot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
)

// fakeRuntimeConfig parametrizes the fakeRuntime constructor. PTY toggles
// the declared Caps.PTY (which agent.Boot uses to drive shouldUsePTY's
// per-Mode + per-provider matrix); StartErr / WaitExitErr drive failure
// injection. Note Supervisor / ResourceLimits flow regardless of PTY —
// profileSupervision pass-through is unconditional (see
// decisions.torque.supervisor_passthrough_on_adapter_path).
type fakeRuntimeConfig struct {
	// PTY becomes Caps.PTY. Tests pick true for ModeLongLived/Subagent/
	// Background/Resume coverage and false for OneShot.
	PTY bool

	// StartErr, when non-nil, is returned from Runtime.Start. Used to
	// exercise Boot's rollback path in a future TestBoot_StartFailRollsBack
	// test (out of scope for the current ticket).
	StartErr error

	// WaitExitErr, when non-nil, is returned from fakeSession.Wait wrapped
	// as the second return value. Used by TestBoot_ExitErrorCausePropagation
	// to assert callers can extract Cause via errors.As.
	WaitExitErr *agentsessions.ExitError

	// SuppressTurnDoneOnSendInput, when true, makes fakeSession.SendInput
	// skip the llmtypes.EventDone emission. Used by tests that exercise
	// agent.Boot's ModeOneShot timeout fall-through (ctx.Done() wins the
	// select because no turn-complete signal ever arrives).
	SuppressTurnDoneOnSendInput bool

	// JsonRpcResponses scripts canned per-method responses for the
	// fakeSession.JsonRpcCaller.Call surface. Used by codex JsonRpcStdio
	// tests to drive the {thread: {id}} envelope torque's SendTurn
	// decodes on the first turn. Methods without an entry get a {}
	// response (good enough for initialize + turn/start which torque
	// doesn't inspect the result body of).
	JsonRpcResponses map[string]json.RawMessage

	// RuntimeFactoryErr, when non-nil, makes Dependencies.RuntimeFactory
	// return it instead of a fakeRuntime. agent.Boot invokes the factory
	// AFTER providerplant.Plant materializes the boot dir, so this drives
	// the "construct runtime" intermediate-failure path — used by the
	// boot-dir leak regression test to assert the planted dir is reaped.
	RuntimeFactoryErr error
}

// newFakeRuntime constructs a fakeRuntime backed by the given config. The
// returned runtime is safe for concurrent use; tests typically construct
// one per Boot call so each captures its own StartOptions.
func newFakeRuntime(cfg fakeRuntimeConfig) *fakeRuntime {
	rt := &fakeRuntime{
		id:   "fake-runtime",
		kind: "fake",
		caps: agentsessions.Capabilities{
			PTY:               cfg.PTY,
			Resize:            cfg.PTY,
			ProviderSessionID: true,
			CheckpointResume:  true,
		},
		startErr:                    cfg.StartErr,
		waitExitErr:                 cfg.WaitExitErr,
		suppressTurnDoneOnSendInput: cfg.SuppressTurnDoneOnSendInput,
		jsonRpcResponses:            cfg.JsonRpcResponses,
	}
	rt.pidNext.Store(3000)
	return rt
}

// fakeRuntime is the test-injected agentsessions.Runtime. Each Start call
// allocates a fakeSession and records the StartOptions for later assertion.
//
// Capability choice rationale: PTY is per-test (controls Boot's
// profileSupervision branch). ProviderSessionID + CheckpointResume default
// true so the Boot path through findCheckpoint + OnSessionID is exercised.
// BinaryRequired is false — the fake runtime has no Detect() to call;
// Prepare() always succeeds.
type fakeRuntime struct {
	id   string
	kind string
	caps agentsessions.Capabilities

	// adapter is the per-runtime CLIAdapter the factory was constructed
	// with. Used to simulate the lib's preparePlant under
	// AutoPlantBootDir — the fake walks adapter.BootDirSpec().PlantedFiles
	// the same way agentsessions.preparePlant does, so e2e tests observe
	// the same Session.BootDir + planted file shape they got pre-AutoPlant.
	// Set by composeDeps in helpers.go.
	adapter provider.CLIAdapter

	pidNext atomic.Int32

	// startErr / waitExitErr drive failure injection.
	startErr    error
	waitExitErr *agentsessions.ExitError

	// suppressTurnDoneOnSendInput, when true, drops the synthetic
	// llmtypes.EventDone emission from fakeSession.SendInput. Used by
	// agent.Boot's ModeOneShot timeout-fall-through test (no turn-complete
	// signal → select must wait for ctx.Done()).
	suppressTurnDoneOnSendInput bool

	// jsonRpcResponses propagates from fakeRuntimeConfig.JsonRpcResponses
	// to every fakeSession this runtime spawns. Used by JsonRpcStdio
	// tests to script thread/start's {thread:{id}} envelope.
	jsonRpcResponses map[string]json.RawMessage

	// Captured StartOptions snapshot — populated on every Start() call.
	// Tests read after Boot returns; access is guarded by atomic.Pointer
	// so the read goroutine sees a consistent snapshot.
	lastStartOpts atomic.Pointer[agentsessions.StartOptions]

	// Convenience extractors for the most commonly asserted StartOptions
	// fields. Mirror the lastStartOpts snapshot; tests can use either form.
	autoFireFirstTurn     atomic.Bool
	firstTurnPayload      atomic.Pointer[[]byte]
	workspaceDir          atomic.Pointer[string]
	sessionIDPreset       atomic.Pointer[string]
	allowLoopback         atomic.Bool
	supervisorPresent     atomic.Bool
	resourceLimitsPresent atomic.Bool
	typedEventCallbackSet atomic.Bool

	// sessions tracks every fakeSession returned by Start. Tests that need
	// to drive lifecycle (simulate typed events, force exit errors) reach
	// for the most-recent via lastSession(); cleanup walks the full slice
	// so multi-session tests (e.g. broker hello-world) drain every session
	// before Manager.Shutdown.
	mu       sync.Mutex
	sessions []*fakeSession
}

func (r *fakeRuntime) ID() string                       { return r.id }
func (r *fakeRuntime) Kind() string                     { return r.kind }
func (r *fakeRuntime) Caps() agentsessions.Capabilities { return r.caps }
func (r *fakeRuntime) Prepare(_ context.Context) error  { return nil }

// Start records opts and returns a fresh fakeSession. Note: this fake
// intentionally does NOT auto-fire SendInput when AutoFireFirstTurn=true —
// real runtimes do (per agentsessions.NewFromAdapter v0.6.0), but the
// per-Mode tests assert on the captured StartOptions field rather than
// observing a SendInput call. Treating AutoFireFirstTurn as Boot-side
// metadata keeps sendInputCalls clean for the "Boot must NOT call SendInput
// separately" assertion.
func (r *fakeRuntime) Start(_ context.Context, opts agentsessions.StartOptions) (agentsessions.Session, error) {
	if r.startErr != nil {
		return nil, r.startErr
	}

	// Snapshot StartOptions. Copy the byte slices so the caller can mutate
	// them without affecting test reads.
	snap := opts
	r.lastStartOpts.Store(&snap)
	r.autoFireFirstTurn.Store(opts.AutoFireFirstTurn)
	if opts.FirstTurnPayload != nil {
		cp := append([]byte(nil), opts.FirstTurnPayload...)
		r.firstTurnPayload.Store(&cp)
	}
	if opts.WorkspaceDir != "" {
		ws := opts.WorkspaceDir
		r.workspaceDir.Store(&ws)
	}
	if opts.SessionIDPreset != "" {
		sip := opts.SessionIDPreset
		r.sessionIDPreset.Store(&sip)
	}
	r.allowLoopback.Store(opts.Profile.AllowLoopback)
	r.supervisorPresent.Store(opts.Supervisor != nil)
	r.resourceLimitsPresent.Store(opts.ResourceLimits != nil && !opts.ResourceLimits.IsZero())
	r.typedEventCallbackSet.Store(opts.TypedEventCallback != nil)

	// Simulate the lib's preparePlant when AutoPlantBootDir is on. The
	// fake doesn't go through agentsessions.NewFromAdapter, so the lib's
	// real preparePlant never fires for these tests. Replicating the
	// behavior here keeps Session.BootDir + planted-file assertions
	// (e.g. apikey_helper_test) accurate against the post-AutoPlantBootDir
	// substrate. Match the lib's preparePlant in agentsessions/bootdir_planting.go.
	var plantedBootDir string
	if opts.AutoPlantBootDir && r.adapter != nil {
		bp, ok := r.adapter.(provider.BootDirProvider)
		if ok {
			spec := bp.BootDirSpec()
			if len(spec.PlantedFiles) > 0 {
				root := opts.BootDirRoot
				if root == "" && opts.WorkspaceDir != "" {
					root = filepath.Join(opts.WorkspaceDir, "boot")
				}
				if root == "" {
					root = os.TempDir()
				}
				if err := os.MkdirAll(root, 0o750); err != nil {
					return nil, fmt.Errorf("fakeRuntime: ensure boot root %s: %w", root, err)
				}
				dir, err := os.MkdirTemp(root, "agent-sessions-boot-fake-*")
				if err != nil {
					return nil, fmt.Errorf("fakeRuntime: create boot dir: %w", err)
				}
				bootContent := opts.BootContent
				if bootContent == "" {
					bootContent = opts.BootPrompt
				}
				plantCtx := opts.PlantContext
				plantCtx.SystemPrompt = opts.BootPrompt
				plantCtx.BootContent = bootContent
				plantCtx.ProjectDir = opts.Workdir
				plantCtx.BootDir = dir
				for _, pf := range spec.PlantedFiles {
					path := filepath.Join(dir, pf.RelPath)
					if mkErr := os.MkdirAll(filepath.Dir(path), 0o750); mkErr != nil {
						_ = os.RemoveAll(dir)
						return nil, fmt.Errorf("fakeRuntime: plant %s: mkdir: %w", pf.RelPath, mkErr)
					}
					if pf.Render == nil {
						continue
					}
					content, rerr := pf.Render(plantCtx)
					if rerr != nil {
						_ = os.RemoveAll(dir)
						return nil, fmt.Errorf("fakeRuntime: plant %s: render: %w", pf.RelPath, rerr)
					}
					mode := pf.Mode
					if mode == 0 {
						if pf.RelPath == ".mcp.json" || filepath.Base(pf.RelPath) == "settings.json" {
							mode = 0o600
						} else {
							mode = 0o644
						}
					}
					if werr := os.WriteFile(path, []byte(content), mode); werr != nil {
						_ = os.RemoveAll(dir)
						return nil, fmt.Errorf("fakeRuntime: plant %s: write: %w", pf.RelPath, werr)
					}
				}
				plantedBootDir = dir
				if opts.OnBootDirPlanted != nil {
					opts.OnBootDirPlanted(dir)
				}
			}
		}
	}

	pid := int(r.pidNext.Add(1))
	sess := newFakeSession(pid, opts.TypedEventCallback, opts.OnSessionID, r.waitExitErr)
	sess.bootDir = plantedBootDir
	sess.notificationHook = opts.JsonRpcNotificationHook
	sess.jsonRpcResponses = r.jsonRpcResponses
	if !r.suppressTurnDoneOnSendInput {
		sess.eventFanout = opts.EventFanout
	}

	r.mu.Lock()
	r.sessions = append(r.sessions, sess)
	r.mu.Unlock()
	return sess, nil
}

// lastSession returns the most-recent fakeSession returned by Start, or nil
// when no Start has been called.
func (r *fakeRuntime) lastSession() *fakeSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sessions) == 0 {
		return nil
	}
	return r.sessions[len(r.sessions)-1]
}

// allSessions returns a snapshot of every fakeSession Start has returned,
// in spawn order. Cleanup walks this so tests that boot more than one
// session (e.g. TestSubstrate_HelloWorld's two long-lived sessions plus
// their resumed counterparts) drain each fakeSession.done before
// Manager.Shutdown — without this, only the most-recent session's watch
// goroutine would unblock.
func (r *fakeRuntime) allSessions() []*fakeSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*fakeSession, len(r.sessions))
	copy(out, r.sessions)
	return out
}

// simulateTypedEvent fires the captured TypedEventCallback against the
// most-recent fakeSession's recorded callback. No-op when no callback was
// registered. Used by TestBoot_TypedEventCallback_FiresOnPTYPath to drive
// the callback synthetically without actually spawning claude.
func (r *fakeRuntime) simulateTypedEvent(ev events.Event) {
	sess := r.lastSession()
	if sess == nil || sess.typedEventCB == nil {
		return
	}
	sess.typedEventCB(ev)
	sess.typedEventHits.Add(1)
}

// fakeSession is the inert agentsessions.Session returned by fakeRuntime.
// It records SendInput calls + delivers TypedEventCallback events on demand
// + reports a (mutable) PID via PIDReporter. Lifecycle is driven directly by
// tests (Stop closes the done chan; Wait surfaces waitExitErr; setPID flips
// the live PID mid-session so the PID poller has something to observe).
type fakeSession struct {
	pid          atomic.Int32
	typedEventCB provider.EventsCallback
	onSessionID  func(string)
	waitExitErr  *agentsessions.ExitError

	sendInputCalls atomic.Int32
	typedEventHits atomic.Int32

	mu               sync.Mutex
	sendInputPayload [][]byte // last-N payloads for assertion convenience

	// bootDir, when non-empty, was materialized by fakeRuntime.Start's
	// preparePlant simulation. Cleared on Stop to mirror the lib's
	// terminal-state cleanup contract (cleanupBootDir).
	bootDir string

	// eventFanout, when non-nil, captures the StartOptions.EventFanout
	// channel so SendInput can mirror the real adapter/streaming sessions
	// by emitting an llmtypes.EventDone event after the turn payload
	// lands. JsonRpcStdio sessions skip the EventDone emit (the codex
	// app-server adapter's ParseLine returns nil; the lib fans no
	// events through EventFanout for that runtime kind) — turn-complete
	// is signalled via the notificationHook instead, fired from
	// fakeSession.Call on `turn/start`.
	eventFanout chan<- llmtypes.StreamEvent

	// notificationHook captures StartOptions.JsonRpcNotificationHook so
	// fakeSession.Call can emit a `turn/completed` notification after
	// recording a turn/start invocation. Mirrors the real codex
	// app-server's notification stream — every successful turn ends with
	// a turn/completed notification which agent.Boot ModeOneShot
	// listens for to detect turn-complete.
	notificationHook func(string, json.RawMessage)

	// jsonRpcCalls records each JsonRpcCaller.Call invocation in order
	// for test assertions. Each entry is {method, params}. Guarded by
	// mu (the same lock that protects sendInputPayload).
	jsonRpcCalls []recordedJsonRpcCall

	// jsonRpcResponses, when non-nil, scripts canned responses per
	// JSON-RPC method. Used by tests to drive thread/start's response
	// shape (the {thread: {id}} envelope torque's SendTurn decodes).
	jsonRpcResponses map[string]json.RawMessage

	done     chan struct{}
	doneOnce sync.Once
	dead     atomic.Bool
}

// recordedJsonRpcCall captures one JsonRpcCaller.Call invocation for
// test assertions. Params is stored as the raw any so tests can
// type-assert and inspect specific fields (e.g. turn/start's threadId).
type recordedJsonRpcCall struct {
	Method string
	Params any
}

func newFakeSession(pid int, cb provider.EventsCallback, onSessionID func(string), waitErr *agentsessions.ExitError) *fakeSession {
	s := &fakeSession{
		typedEventCB: cb,
		onSessionID:  onSessionID,
		waitExitErr:  waitErr,
		done:         make(chan struct{}),
	}
	s.pid.Store(int32(pid))
	return s
}

// setPID flips the reported live PID. Mirrors the lib's adapterSession
// behavior where runner.EventProcessStarted bumps s.pid mid-turn and
// runner.EventProcessExited resets it to 0. Used by PID-poller tests to
// drive observable transitions.
func (s *fakeSession) setPID(pid int) { s.pid.Store(int32(pid)) }

// Wait blocks until Stop is called. Returns the captured ExitError (when
// set) wrapped as a Go error so callers can extract Cause via errors.As.
func (s *fakeSession) Wait() (int, error) {
	<-s.done
	if s.waitExitErr != nil {
		return s.waitExitErr.Code, s.waitExitErr
	}
	return 0, nil
}

func (s *fakeSession) Stop(_ context.Context) error {
	s.dead.Store(true)
	s.doneOnce.Do(func() {
		close(s.done)
		// Mirror the lib's terminal-state cleanupBootDir contract.
		if s.bootDir != "" {
			_ = os.RemoveAll(s.bootDir)
		}
	})
	return nil
}

func (s *fakeSession) SendInput(_ context.Context, data []byte) error {
	if s.dead.Load() {
		return agentsessions.ErrNoInputChannel
	}
	s.sendInputCalls.Add(1)
	cp := append([]byte(nil), data...)
	s.mu.Lock()
	s.sendInputPayload = append(s.sendInputPayload, cp)
	s.mu.Unlock()
	// Mirror the lib's adapter / streaming-stdio sessions: every turn
	// finishes with an llmtypes.EventDone event. agent.Boot's ModeOneShot
	// path watches the stream fanout for this signal to detect
	// turn-complete on long-lived adapters. Non-blocking send so a closed
	// or full consumer never wedges the fake — the recover guards against
	// send-on-closed (fanout closed by Boot's defer before SendInput races
	// in).
	if s.eventFanout != nil {
		func() {
			defer func() { _ = recover() }()
			select {
			case s.eventFanout <- llmtypes.StreamEvent{Type: llmtypes.EventDone}:
			default:
			}
		}()
	}
	return nil
}

// Call implements agentsessions.JsonRpcCaller so codex JsonRpcStdio
// tests can exercise SendTurn's full handshake. The fake records each
// invocation (method + params) for assertions, returns scripted
// responses when jsonRpcResponses has a match, and fires the
// `turn/completed` notification hook on turn/start (mirroring the
// codex app-server's natural notification stream — every successful
// turn ends with turn/completed). Methods without a scripted response
// return {} (the empty JSON object) so the SendTurn flow doesn't trip
// on missing data; tests that care should set jsonRpcResponses
// explicitly.
func (s *fakeSession) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	if s.dead.Load() {
		return nil, agentsessions.ErrNoInputChannel
	}
	s.mu.Lock()
	s.jsonRpcCalls = append(s.jsonRpcCalls, recordedJsonRpcCall{Method: method, Params: params})
	resp, scripted := s.jsonRpcResponses[method]
	s.mu.Unlock()
	if !scripted {
		resp = json.RawMessage(`{}`)
	}
	// Fire the notification hook AFTER recording but BEFORE returning,
	// so observers see the call before the turn/completed notification
	// (mirrors the codex app-server: the RPC response and the
	// turn/completed notification both arrive after the turn finishes,
	// but the notification is what signals turn-complete to consumers).
	if method == "turn/start" && s.notificationHook != nil {
		s.notificationHook("turn/completed", json.RawMessage(`{}`))
	}
	return resp, nil
}

// recordedJsonRpcCalls returns a snapshot of every JsonRpcCaller.Call
// invocation observed so far. Used by tests asserting on the
// initialize → thread/start → turn/start handshake sequence.
func (s *fakeSession) recordedJsonRpcCalls() []recordedJsonRpcCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.jsonRpcCalls) == 0 {
		return nil
	}
	out := make([]recordedJsonRpcCall, len(s.jsonRpcCalls))
	copy(out, s.jsonRpcCalls)
	return out
}

func (s *fakeSession) Resize(_ context.Context, _, _ uint16) error { return nil }

func (s *fakeSession) Health() agentsessions.HealthStatus {
	state := agentsessions.LiveStateIdle
	if s.dead.Load() {
		state = agentsessions.LiveStateStopped
	}
	return agentsessions.HealthStatus{
		Alive: !s.dead.Load(),
		PID:   int(s.pid.Load()),
		State: state,
	}
}

func (s *fakeSession) CheckpointHints() (agentsessions.CheckpointHint, bool) {
	return nil, false
}

// LivePID / LastPID — implements agentsessions.PIDReporter. Returns 0 once
// Stop has been called (live process gone) but LastPID retains the spawned
// pid for log-correlation parity with the lib's adapterRuntime semantics.
func (s *fakeSession) LivePID() int {
	if s.dead.Load() {
		return 0
	}
	return int(s.pid.Load())
}

func (s *fakeSession) LastPID() int { return int(s.pid.Load()) }

// recordedSendInputs returns a copy of the SendInput payloads observed so
// far. Test-side accessor — fakeSession.mu is the synchronization point.
func (s *fakeSession) recordedSendInputs() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.sendInputPayload))
	for i, p := range s.sendInputPayload {
		cp := append([]byte(nil), p...)
		out[i] = cp
	}
	return out
}

// recordedSendInputCount is the cheap form of recordedSendInputs — returns
// just the call count without copying the payloads.
func (s *fakeSession) recordedSendInputCount() int32 {
	return s.sendInputCalls.Load()
}

// triggerSessionID fires the captured OnSessionID callback (when non-nil).
// Used by TestBoot_ModeResume_LoadsCheckpoint et al when a test wants to
// simulate the adapter observing a provider session id mid-turn.
func (s *fakeSession) triggerSessionID(id string) {
	if s.onSessionID != nil {
		s.onSessionID(id)
	}
}

// simulateExit terminates the session without going through Manager.Stop —
// the lib's Stop sets killing=true on the entry, which forces StateDone
// regardless of exit code. Tests that need StateFailed (e.g. ExitError +
// non-zero exit) close fakeSession.done directly so the watch goroutine
// observes the natural termination path.
func (s *fakeSession) simulateExit() {
	s.dead.Store(true)
	s.doneOnce.Do(func() { close(s.done) })
}
