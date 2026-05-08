package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/oklog/ulid/v2"
)

// IDFunc lets tests pin session ids. Production uses ULIDs.
type IDFunc func() string

// defaultSessionID generates a ULID-prefixed session ID. Same source as the
// service-layer's Checkpoint and the legacy sessionmgr used.
func defaultSessionID() string {
	return "SES-" + ulid.Make().String()
}

// Manager owns the lifecycle of registered sessions and proxies the
// agentsessions.Manager surface (SendInput/Resize/Stop/Wait/Attach) with
// clockwork-side persistence and event emission.
//
// Replaces internal/runtime/sessionmgr/Manager. The Boot pattern moves
// Launch from Manager onto the package-level agent.Boot() entry point;
// Manager keeps everything Boot doesn't (lifecycle proxy, checkpoint /
// resume, orphan sweep on startup, per-session loopback teardown).
type Manager struct {
	deps  *Dependencies
	idFn  IDFunc
	nowFn func() time.Time

	mu        sync.RWMutex
	stopped   bool
	inner     *agentsessions.Manager
	loopbacks map[string]*loopbackHandle // sessID → handle; shut down in Stop
	stderrs   map[string]func()          // sessID → close() for the per-session sidecar
}

// NewManager constructs a Manager bound to deps. Caller invokes Sweep()
// once after construction to reconcile orphan rows from prior daemon
// processes.
func NewManager(deps *Dependencies) *Manager {
	if deps == nil {
		// Allow nil for compile-time-test paths; production constructs
		// via the bootstrap composition root with all collaborators set.
		deps = &Dependencies{}
	}
	m := &Manager{
		deps:      deps,
		idFn:      defaultSessionID,
		nowFn:     time.Now,
		loopbacks: make(map[string]*loopbackHandle),
		stderrs:   make(map[string]func()),
	}
	emitter := NewSchedulerEmitter(deps.Bus)
	stateSink := &storeStateSink{store: deps.Store}
	eventSink := &busEventSink{
		events:     emitter,
		onTerminal: m.teardownSession,
	}
	m.inner = agentsessions.NewManager(stateSink).WithEventSink(eventSink)
	return m
}

// WithIDFunc returns m with a test-supplied id generator. Must be called
// before any Boot; not goroutine-safe with concurrent Boot.
func (m *Manager) WithIDFunc(fn IDFunc) *Manager {
	if fn != nil {
		m.idFn = fn
	}
	return m
}

// WithNowFunc returns m with a test-supplied clock.
func (m *Manager) WithNowFunc(fn func() time.Time) *Manager {
	if fn != nil {
		m.nowFn = fn
	}
	return m
}

// inner returns the wrapped agentsessions.Manager so package-internal Boot
// can call Start without going through a public surface.
func (m *Manager) innerManager() *agentsessions.Manager {
	return m.inner
}

// registerLoopback associates a per-session loopback handle so Stop / Wait
// completion can shut it down deterministically. Caller takes ownership of
// the lifetime when registerLoopback returns; Boot for ModeOneShot bypasses
// this registration and shuts the handle down inline (synchronous lifecycle).
func (m *Manager) registerLoopback(sessID string, h *loopbackHandle) {
	if h == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loopbacks[sessID] = h
}

// registerStderrCloser associates a per-session stderr-sidecar closer so
// Stop / sweep can flush + close the file. Same lifetime contract as
// registerLoopback.
func (m *Manager) registerStderrCloser(sessID string, closer func()) {
	if closer == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stderrs[sessID] = closer
}

// teardownSession runs the per-session cleanups (loopback shutdown, stderr
// sidecar close). Idempotent. Called from Stop and from the watch goroutine
// when the session reaches a terminal state.
func (m *Manager) teardownSession(sessID string) {
	m.mu.Lock()
	loopback := m.loopbacks[sessID]
	delete(m.loopbacks, sessID)
	closer := m.stderrs[sessID]
	delete(m.stderrs, sessID)
	m.mu.Unlock()
	if loopback != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = loopback.Shutdown(shutdownCtx)
		cancel()
	}
	if closer != nil {
		closer()
	}
}

// Get returns the persisted session record. Combines store state with any
// live in-memory state from the inner manager (PID, attached clients).
func (m *Manager) Get(id string) (*Session, error) {
	if m.deps.Store == nil {
		return nil, fmt.Errorf("agent.Manager.Get: nil store")
	}
	rec, err := m.deps.Store.GetSession(id)
	if err != nil {
		if errors.Is(err, sqlstore.ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	sess := sessionFromRecord(rec)
	if info, ok := m.inner.Get(id); ok {
		sess.PID = info.PID
	}
	return sess, nil
}

// List returns persisted sessions (newest-first) matching the filter.
func (m *Manager) List(state Status, taskID, projectID string, limit int) ([]*Session, error) {
	if m.deps.Store == nil {
		return nil, fmt.Errorf("agent.Manager.List: nil store")
	}
	recs, err := m.deps.Store.ListSessions(sqlstore.SessionFilter{
		State:     string(state),
		TaskID:    taskID,
		ProjectID: projectID,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*Session, 0, len(recs))
	for _, rec := range recs {
		out = append(out, sessionFromRecord(rec))
	}
	return out, nil
}

// SendInput forwards a payload to the named session's input stream.
func (m *Manager) SendInput(id string, data []byte) error {
	if err := m.checkStopped(); err != nil {
		return err
	}
	if err := m.inner.SendInput(id, data); err != nil {
		if errors.Is(err, agentsessions.ErrSessionNotRunning) {
			return ErrSessionNotRunning
		}
		return err
	}
	if m.deps.Store != nil {
		_ = m.deps.Store.TouchSession(id)
	}
	return nil
}

// Resize forwards a (rows, cols) winsize update.
func (m *Manager) Resize(id string, rows, cols uint16) error {
	if err := m.checkStopped(); err != nil {
		return err
	}
	if err := m.inner.Resize(id, rows, cols); err != nil {
		if errors.Is(err, agentsessions.ErrSessionNotRunning) {
			return ErrSessionNotRunning
		}
		return err
	}
	return nil
}

// Attach subscribes w to the session's live output stream. AttachEnabled
// must have been set on the boot options for the inner manager to allocate
// a broker; agent.Boot wires AttachEnabled=true uniformly so any session
// can be attached after Boot returns.
func (m *Manager) Attach(ctx context.Context, id string, w io.Writer) error {
	if err := m.checkStopped(); err != nil {
		return err
	}
	if err := m.inner.Attach(ctx, id, w); err != nil {
		switch {
		case errors.Is(err, agentsessions.ErrSessionNotRunning):
			return ErrSessionNotRunning
		case errors.Is(err, agentsessions.ErrAttachDisabled):
			return errors.New("agent: attach disabled for this session")
		}
		return err
	}
	return nil
}

// Stop signals the session to terminate. The watch goroutine handles the
// terminal-state record asynchronously; per-session loopback + stderr
// cleanups run synchronously here so the next Start can rebind 127.0.0.1
// without waiting for the watch goroutine.
func (m *Manager) Stop(ctx context.Context, id string) error {
	defer m.teardownSession(id)
	if err := m.inner.Stop(ctx, id); err != nil {
		if errors.Is(err, agentsessions.ErrSessionNotRunning) {
			// Live in DB but not in-memory (manager restarted). Mark failed
			// defensively so the dashboard moves on.
			if m.deps.Store != nil {
				if rec, getErr := m.deps.Store.GetSession(id); getErr == nil && !Status(rec.State).Terminal() {
					exit := -1
					_ = m.deps.Store.UpdateSessionState(id, string(StatusFailed), 0, &exit)
				}
			}
			return ErrSessionNotRunning
		}
		return err
	}
	return nil
}

// Wait blocks until the session terminates. Returns the exit code.
func (m *Manager) Wait(ctx context.Context, id string) (int, error) {
	code, err := m.inner.WaitSession(ctx, id)
	if err != nil {
		if errors.Is(err, agentsessions.ErrSessionNotRunning) {
			return 0, ErrSessionNotRunning
		}
		return 0, err
	}
	return code, nil
}

// Checkpoint persists a checkpoint snapshot for the session.
func (m *Manager) Checkpoint(req CheckpointRequest) (*Checkpoint, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("agent.Manager.Checkpoint: SessionID required")
	}
	if m.deps.Store == nil {
		return nil, fmt.Errorf("agent.Manager.Checkpoint: nil store")
	}
	if _, err := m.deps.Store.GetSession(req.SessionID); err != nil {
		if errors.Is(err, sqlstore.ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	cpID := "SCP-" + ulid.Make().String()
	rec := &sqlstore.SessionCheckpointRecord{
		ID:        cpID,
		SessionID: req.SessionID,
		Payload:   req.Payload,
		Note:      req.Note,
	}
	if err := m.deps.Store.CreateSessionCheckpoint(rec); err != nil {
		return nil, fmt.Errorf("create session checkpoint: %w", err)
	}
	_ = m.deps.Store.TouchSession(req.SessionID)
	return &Checkpoint{
		ID:        cpID,
		SessionID: req.SessionID,
		Payload:   rec.Payload,
		Note:      req.Note,
		CreatedAt: m.nowFn().UTC(),
	}, nil
}

// ListCheckpoints returns checkpoints for the named session, newest-first.
func (m *Manager) ListCheckpoints(sessionID string, limit int) ([]*Checkpoint, error) {
	if m.deps.Store == nil {
		return nil, fmt.Errorf("agent.Manager.ListCheckpoints: nil store")
	}
	recs, err := m.deps.Store.ListSessionCheckpoints(sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*Checkpoint, 0, len(recs))
	for _, r := range recs {
		out = append(out, &Checkpoint{
			ID:         r.ID,
			SessionID:  r.SessionID,
			Payload:    r.Payload,
			ResumeHint: r.ResumeHint,
			Note:       r.Note,
			CreatedAt:  r.CreatedAt,
		})
	}
	return out, nil
}

// Sweep marks `launching` and `running` rows whose process is gone as
// `crashed`. Single call at startup. Returns the number of rows transitioned.
//
// Forked from internal/runtime/sessionmgr/manager.go's Sweep.
func (m *Manager) Sweep() (int, error) {
	if m.deps.Store == nil {
		return 0, nil
	}
	return m.deps.Store.SweepStaleSessions(func(rec *sqlstore.SessionRecord) bool {
		if rec.PID <= 0 {
			return false
		}
		err := syscall.Kill(rec.PID, syscall.Signal(0))
		switch {
		case err == nil:
			return true // alive — spare it
		case errors.Is(err, os.ErrPermission):
			return true // visible but not ours; assume alive
		default:
			return false // ESRCH or other — treat as dead
		}
	})
}

// Shutdown stops accepting new boots and waits for in-flight watch
// goroutines to drain.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil
	}
	m.stopped = true
	m.mu.Unlock()
	return m.inner.Shutdown(ctx)
}

func (m *Manager) checkStopped() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.stopped {
		return ErrManagerStopped
	}
	return nil
}

// LivePID reports the current PID of the named session's running process.
// Returns 0 between turns (subprocess-per-turn) or for terminated sessions.
func (m *Manager) LivePID(id string) int {
	info, ok := m.inner.Get(id)
	if !ok {
		return 0
	}
	return info.PID
}

// providerFromRuntime extracts a stable provider token from a Runtime ID
// (convention: "clockwork-cli/<provider>").
func providerFromRuntime(rt agentsessions.Runtime) string {
	id := rt.ID()
	const prefix = "clockwork-cli/"
	if len(id) > len(prefix) && id[:len(prefix)] == prefix {
		return id[len(prefix):]
	}
	return id
}

func encodeMeta(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeMeta(s string) map[string]string {
	if s == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// sessionFromRecord projects a sqlstore row onto the public Session shape.
func sessionFromRecord(rec *sqlstore.SessionRecord) *Session {
	s := &Session{
		ID:           rec.ID,
		AgentProfile: rec.AgentProfile,
		Provider:     rec.Provider,
		RuntimeID:    rec.RuntimeID,
		RuntimeKind:  rec.RuntimeKind,
		Workdir:      rec.Workdir,
		Status:       Status(rec.State),
		PID:          rec.PID,
		ResumeHint:   rec.ResumeHint,
		Meta:         decodeMeta(rec.MetaJSON),
		CreatedAt:    rec.CreatedAt,
		UpdatedAt:    rec.UpdatedAt,
		LastActivity: rec.LastActivity,
	}
	if rec.ProjectID.Valid {
		s.ProjectID = rec.ProjectID.String
	}
	if rec.TaskID.Valid {
		s.TaskID = rec.TaskID.String
	}
	if rec.ExitCode.Valid {
		v := int(rec.ExitCode.Int64)
		s.ExitCode = &v
	}
	if rec.EndedAt.Valid {
		t := rec.EndedAt.Time
		s.EndedAt = &t
	}
	return s
}
