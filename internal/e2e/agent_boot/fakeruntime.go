package agent_boot

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
)

// fakeRuntimeConfig parametrizes the fakeRuntime constructor. PTY toggles
// the declared Caps.PTY (which agent.Boot uses to decide whether to wire
// Supervisor/ResourceLimits via profileSupervision); StartErr / WaitExitErr
// drive failure injection.
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
		startErr:     cfg.StartErr,
		waitExitErr:  cfg.WaitExitErr,
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

	pidNext atomic.Int32

	// startErr / waitExitErr drive failure injection.
	startErr    error
	waitExitErr *agentsessions.ExitError

	// Captured StartOptions snapshot — populated on every Start() call.
	// Tests read after Boot returns; access is guarded by atomic.Pointer
	// so the read goroutine sees a consistent snapshot.
	lastStartOpts atomic.Pointer[agentsessions.StartOptions]

	// Convenience extractors for the most commonly asserted StartOptions
	// fields. Mirror the lastStartOpts snapshot; tests can use either form.
	autoFireFirstTurn      atomic.Bool
	firstTurnPayload       atomic.Pointer[[]byte]
	workspaceDir           atomic.Pointer[string]
	sessionIDPreset        atomic.Pointer[string]
	allowLoopback          atomic.Bool
	supervisorPresent      atomic.Bool
	resourceLimitsPresent  atomic.Bool
	typedEventCallbackSet  atomic.Bool

	// liveSession is the most-recent fakeSession returned by Start. Tests
	// that need to drive lifecycle (simulate typed events, force exit
	// errors) reach for it via lastSession().
	mu          sync.Mutex
	liveSession *fakeSession
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

	pid := int(r.pidNext.Add(1))
	sess := newFakeSession(pid, opts.TypedEventCallback, opts.OnSessionID, r.waitExitErr)

	r.mu.Lock()
	r.liveSession = sess
	r.mu.Unlock()
	return sess, nil
}

// lastSession returns the most-recent fakeSession returned by Start, or nil
// when no Start has been called.
func (r *fakeRuntime) lastSession() *fakeSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.liveSession
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
// + reports a stable PID via PIDReporter. Lifecycle is driven directly by
// tests (Stop closes the done chan; Wait surfaces waitExitErr).
type fakeSession struct {
	pid          int
	typedEventCB provider.EventsCallback
	onSessionID  func(string)
	waitExitErr  *agentsessions.ExitError

	sendInputCalls atomic.Int32
	typedEventHits atomic.Int32

	mu               sync.Mutex
	sendInputPayload [][]byte // last-N payloads for assertion convenience

	done     chan struct{}
	doneOnce sync.Once
	dead     atomic.Bool
}

func newFakeSession(pid int, cb provider.EventsCallback, onSessionID func(string), waitErr *agentsessions.ExitError) *fakeSession {
	return &fakeSession{
		pid:          pid,
		typedEventCB: cb,
		onSessionID:  onSessionID,
		waitExitErr:  waitErr,
		done:         make(chan struct{}),
	}
}

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
	s.doneOnce.Do(func() { close(s.done) })
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
	return nil
}

func (s *fakeSession) Resize(_ context.Context, _, _ uint16) error { return nil }

func (s *fakeSession) Health() agentsessions.HealthStatus {
	state := agentsessions.LiveStateIdle
	if s.dead.Load() {
		state = agentsessions.LiveStateStopped
	}
	return agentsessions.HealthStatus{
		Alive: !s.dead.Load(),
		PID:   s.pid,
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
	return s.pid
}

func (s *fakeSession) LastPID() int { return s.pid }

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
