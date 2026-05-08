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
	loopbacks map[string]LoopbackHandle // sessID → handle; shut down in Stop
	stderrs   map[string]func()         // sessID → close() for the per-session sidecar
	bootDirs  map[string]string         // sessID → ephemeral boot dir; os.RemoveAll in Stop
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
		loopbacks: make(map[string]LoopbackHandle),
		stderrs:   make(map[string]func()),
		bootDirs:  make(map[string]string),
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
func (m *Manager) registerLoopback(sessID string, h LoopbackHandle) {
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

// registerBootDir associates the per-task ephemeral tempdir with the session
// so terminal-state observation + explicit Stop can os.RemoveAll it. Without
// this, non-OneShot Modes (LongLived / Subagent / Background / Resume) would
// leak $TMPDIR/clockwork-boot-* directories (with .mcp.json carrying the
// loopback URL) until OS-level housekeeping reclaimed them. ModeOneShot
// continues to clean inline via Boot's defer.
func (m *Manager) registerBootDir(sessID, bootDir string) {
	if bootDir == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bootDirs[sessID] = bootDir
}

// teardownSession runs the per-session cleanups (loopback shutdown, stderr
// sidecar close, ephemeral boot dir removal). Idempotent. Called from Stop
// and from the watch goroutine's terminal-state observation in busEventSink.
func (m *Manager) teardownSession(sessID string) {
	m.mu.Lock()
	loopback := m.loopbacks[sessID]
	delete(m.loopbacks, sessID)
	closer := m.stderrs[sessID]
	delete(m.stderrs, sessID)
	bootDir := m.bootDirs[sessID]
	delete(m.bootDirs, sessID)
	m.mu.Unlock()
	shutdownLoopbackHandle(loopback)
	if closer != nil {
		closer()
	}
	if bootDir != "" {
		// Cleanup failure is non-fatal — the dir lives in $TMPDIR and OS
		// housekeeping reclaims it eventually. Log via the runtime is also
		// non-fatal; we silently swallow.
		_ = os.RemoveAll(bootDir)
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

// Boot is the convenience that drives package-level Boot with this
// Manager's bound Dependencies. HTTP handlers, MCP tools, and planstart
// call this to avoid threading *Dependencies separately. Equivalent to
// agent.Boot(ctx, m.deps, opts).
func (m *Manager) Boot(ctx context.Context, opts Options) (*Session, error) {
	return Boot(ctx, m.deps, opts)
}

// Resume re-launches a session against a previous checkpoint. Wraps Boot
// with Mode=ModeResume; the SessionID/CheckpointID lookup happens inside
// findCheckpoint. Replaces sessionmgr.Manager.Resume.
func (m *Manager) Resume(ctx context.Context, req ResumeRequest) (string, error) {
	if req.SessionID == "" {
		return "", fmt.Errorf("agent.Manager.Resume: SessionID required")
	}
	if m.deps.Store == nil {
		return "", fmt.Errorf("agent.Manager.Resume: nil store")
	}
	src, err := m.deps.Store.GetSession(req.SessionID)
	if err != nil {
		if errors.Is(err, sqlstore.ErrSessionNotFound) {
			return "", ErrSessionNotFound
		}
		return "", err
	}
	// Resolve the checkpoint to thread through Boot. Latest when CheckpointID
	// is empty.
	var cp *sqlstore.SessionCheckpointRecord
	if req.CheckpointID != "" {
		all, err := m.deps.Store.ListSessionCheckpoints(req.SessionID, 0)
		if err != nil {
			return "", err
		}
		for _, c := range all {
			if c.ID == req.CheckpointID {
				cp = c
				break
			}
		}
		if cp == nil {
			return "", ErrNoCheckpoint
		}
	} else {
		cp, err = m.deps.Store.LatestSessionCheckpoint(req.SessionID)
		if err != nil {
			return "", err
		}
		if cp == nil {
			return "", ErrNoCheckpoint
		}
	}

	profile := req.AgentProfile
	if profile == "" {
		profile = src.AgentProfile
	}
	workdir := req.Workdir
	if workdir == "" {
		workdir = src.Workdir
	}

	envMap := make(map[string]string, len(req.Env))
	for _, kv := range req.Env {
		// req.Env is []string of "K=V"; split into the map shape Options
		// expects. Skip malformed entries.
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				envMap[kv[:i]] = kv[i+1:]
				break
			}
		}
	}

	sess, err := Boot(ctx, m.deps, Options{
		Mode:                 ModeResume,
		AgentProfile:         profile,
		Workdir:              workdir,
		ResumeFromCheckpoint: cp.ID,
		SystemPrompt:         req.SystemPrompt,
		Env:                  envMap,
	})
	if err != nil {
		return "", err
	}
	return sess.ID, nil
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

// Reserved keys stamped into SessionMeta by Boot so Get/List can recover the
// fields that don't have dedicated DB columns. The `clockwork.` prefix marks
// them as substrate-internal so caller-supplied SessionMeta keys won't
// collide.
const (
	metaKeyMode            = "clockwork.mode"
	metaKeyBootDir         = "clockwork.boot_dir"
	metaKeyWorkspaceDir    = "clockwork.workspace_dir"
	metaKeyParentSessionID = "clockwork.parent_session_id"
)

// sessionFromRecord projects a sqlstore row onto the public Session shape.
// Decodes the substrate-stamped MetaJSON keys (clockwork.mode, .boot_dir,
// .workspace_dir, .parent_session_id) so Get/List return the same fields
// Boot returns — addresses Copilot review feedback on PR #19 about API
// surface inconsistency.
func sessionFromRecord(rec *sqlstore.SessionRecord) *Session {
	meta := decodeMeta(rec.MetaJSON)
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
		Meta:         meta,
		CreatedAt:    rec.CreatedAt,
		UpdatedAt:    rec.UpdatedAt,
		LastActivity: rec.LastActivity,
	}
	if meta != nil {
		s.Mode = parseModeString(meta[metaKeyMode])
		s.BootDir = meta[metaKeyBootDir]
		s.WorkspaceDir = meta[metaKeyWorkspaceDir]
		s.ParentSessionID = meta[metaKeyParentSessionID]
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
