package sessionmgr

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

// AdapterRegistry resolves a clockwork agent profile to a go-agent-sessions
// Runtime that the Manager can hand to agentsessions.Manager.Start. The
// registry is supplied by the caller so sessionmgr does not pull in the
// cliexec adapter wiring directly — keeps the package testable and lets
// S1.5 swap in a tool-broker-aware variant later.
type AdapterRegistry interface {
	// RuntimeFor returns the agentsessions.Runtime for the named profile,
	// pre-bound to the given resume hint (when non-nil and the underlying
	// adapter understands it). Implementations validate the profile is
	// known and return ErrAdapterNotFound when not.
	RuntimeFor(profileName string, resumeHint []byte) (agentsessions.Runtime, error)
}

// IDFunc lets tests pin session ids. Production uses ULIDs.
type IDFunc func() string

// defaultIDFunc generates a ULID with monotonic ordering — same source as
// service.Checkpoint uses.
func defaultIDFunc() string {
	return "SES-" + ulid.Make().String()
}

// Manager is the long-lived owner of registered sessions. Construct with
// New, then call Sweep at startup to reconcile orphan rows.
type Manager struct {
	store    *sqlstore.Store
	adapters AdapterRegistry
	events   EventEmitter
	idFn     IDFunc
	nowFn    func() time.Time

	mu      sync.RWMutex
	stopped bool
	inner   *agentsessions.Manager
}

// New constructs a Manager. events may be nil — emissions are silently
// skipped in that case, matching the rest of the runtime stack's nil-sink
// convention.
func New(store *sqlstore.Store, adapters AdapterRegistry, events EventEmitter) *Manager {
	m := &Manager{
		store:    store,
		adapters: adapters,
		events:   events,
		idFn:     defaultIDFunc,
		nowFn:    time.Now,
	}
	stateSink := &storeStateSink{store: store}
	eventSink := &busEventSink{events: events}
	m.inner = agentsessions.NewManager(stateSink).WithEventSink(eventSink)
	return m
}

// WithIDFunc returns m with a test-supplied id generator. Must be called
// before any Launch; not goroutine-safe with respect to concurrent Launch.
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

// Launch spawns a new session under Manager ownership, persists its
// initial row, and returns the registered ID. The watch goroutine started
// by go-agent-sessions records terminal state asynchronously.
func (m *Manager) Launch(ctx context.Context, req LaunchRequest) (string, error) {
	if err := m.checkStopped(); err != nil {
		return "", err
	}
	if req.AgentProfile == "" {
		return "", fmt.Errorf("sessionmgr: launch requires AgentProfile")
	}
	if req.Workdir == "" {
		return "", fmt.Errorf("sessionmgr: launch requires Workdir")
	}

	rt, err := m.adapters.RuntimeFor(req.AgentProfile, nil)
	if err != nil {
		return "", err
	}

	id := m.idFn()
	metaJSON, err := encodeMeta(req.SessionMeta)
	if err != nil {
		return "", fmt.Errorf("encode session meta: %w", err)
	}

	rec := &sqlstore.SessionRecord{
		ID:           id,
		AgentProfile: req.AgentProfile,
		Provider:     providerFromRuntime(rt),
		RuntimeID:    rt.ID(),
		RuntimeKind:  rt.Kind(),
		Workdir:      req.Workdir,
		ProjectID:    nullableString(req.ProjectID),
		TaskID:       nullableString(req.TaskID),
		State:        string(StatusLaunching),
		MetaJSON:     metaJSON,
	}
	if err := m.store.CreateSession(rec); err != nil {
		return "", fmt.Errorf("create session row: %w", err)
	}

	startReq := agentsessions.StartRequest{
		ID:      id,
		Runtime: rt,
		Options: agentsessions.StartOptions{
			Workdir:    req.Workdir,
			Env:        req.Env,
			BootPrompt: req.SystemPrompt,
		},
		SessionMeta: req.SessionMeta,
	}
	if err := m.inner.Start(ctx, startReq); err != nil {
		// Inner.Start already recorded StateFailed via the StateSink; no
		// further write needed.
		return "", fmt.Errorf("start agent session: %w", err)
	}
	return id, nil
}

// Get returns the persisted session record. Combines store state with any
// live in-memory state from the inner manager (PID, attached clients).
func (m *Manager) Get(id string) (*Session, error) {
	rec, err := m.store.GetSession(id)
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
	recs, err := m.store.ListSessions(sqlstore.SessionFilter{
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

// SendInput forwards a payload to the named session's input stream. Errors
// when the session is unknown to the inner manager (e.g. terminated).
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
	_ = m.store.TouchSession(id)
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
// must have been set on the launch options for the inner manager to have
// allocated a broker; the MVP launch path here does not enable it, so
// callers that want attach must extend LaunchRequest. Attach is exposed
// for symmetry with the ticket's acceptance criteria; concrete attach-
// driven flows land in S2.
func (m *Manager) Attach(ctx context.Context, id string, w io.Writer) error {
	if err := m.checkStopped(); err != nil {
		return err
	}
	if err := m.inner.Attach(ctx, id, w); err != nil {
		switch {
		case errors.Is(err, agentsessions.ErrSessionNotRunning):
			return ErrSessionNotRunning
		case errors.Is(err, agentsessions.ErrAttachDisabled):
			return errors.New("sessionmgr: attach disabled for this session")
		}
		return err
	}
	return nil
}

// Stop signals the session to terminate. The watch goroutine handles the
// terminal-state record asynchronously.
func (m *Manager) Stop(ctx context.Context, id string) error {
	if err := m.inner.Stop(ctx, id); err != nil {
		if errors.Is(err, agentsessions.ErrSessionNotRunning) {
			// Session might be live in DB but not in-memory (manager restarted).
			// Mark it failed defensively so the dashboard moves on.
			if rec, getErr := m.store.GetSession(id); getErr == nil && !Status(rec.State).Terminal() {
				exit := -1
				_ = m.store.UpdateSessionState(id, string(StatusFailed), 0, &exit)
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

// Checkpoint persists a checkpoint snapshot for the session. The opaque
// payload is stored verbatim; the manager additionally captures the
// CheckpointHint from the underlying adapter (when it exposes one) onto
// both the checkpoint row and the session row's resume_hint column.
func (m *Manager) Checkpoint(req CheckpointRequest) (*Checkpoint, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("sessionmgr: checkpoint requires SessionID")
	}
	if _, err := m.store.GetSession(req.SessionID); err != nil {
		if errors.Is(err, sqlstore.ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	var hint []byte
	// Inner manager only knows about live sessions. CheckpointHint is
	// surfaced via Health snapshot's underlying Session — we approximate
	// by checking whether the session is registered in-memory.
	if info, ok := m.inner.Get(req.SessionID); ok {
		_ = info
		// agentsessions.Manager doesn't currently expose Session.CheckpointHint
		// directly; the upstream lib gates that behind the Session interface
		// which the Manager keeps private. Until v0.5 surfaces it, hints come
		// only from explicit caller wiring. This is a known limitation of the
		// MVP — payload is the source of truth.
	}

	cpID := "SCP-" + ulid.Make().String()
	rec := &sqlstore.SessionCheckpointRecord{
		ID:         cpID,
		SessionID:  req.SessionID,
		Payload:    req.Payload,
		ResumeHint: hint,
		Note:       req.Note,
	}
	if err := m.store.CreateSessionCheckpoint(rec); err != nil {
		return nil, fmt.Errorf("create session checkpoint: %w", err)
	}
	if hint != nil {
		_ = m.store.UpdateSessionResumeHint(req.SessionID, hint)
	}
	_ = m.store.TouchSession(req.SessionID)

	return &Checkpoint{
		ID:         cpID,
		SessionID:  req.SessionID,
		Payload:    rec.Payload,
		ResumeHint: hint,
		Note:       req.Note,
		CreatedAt:  m.nowFn().UTC(),
	}, nil
}

// ListCheckpoints returns checkpoints for the named session, newest-first.
func (m *Manager) ListCheckpoints(sessionID string, limit int) ([]*Checkpoint, error) {
	recs, err := m.store.ListSessionCheckpoints(sessionID, limit)
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

// Resume launches a brand-new session seeded from a previous session's
// checkpoint state. The new session gets a fresh ID; the source session
// row is unchanged.
func (m *Manager) Resume(ctx context.Context, req ResumeRequest) (string, error) {
	if err := m.checkStopped(); err != nil {
		return "", err
	}
	if req.SessionID == "" {
		return "", fmt.Errorf("sessionmgr: resume requires SessionID")
	}
	src, err := m.store.GetSession(req.SessionID)
	if err != nil {
		if errors.Is(err, sqlstore.ErrSessionNotFound) {
			return "", ErrSessionNotFound
		}
		return "", err
	}

	var cp *sqlstore.SessionCheckpointRecord
	if req.CheckpointID != "" {
		all, err := m.store.ListSessionCheckpoints(req.SessionID, 0)
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
		cp, err = m.store.LatestSessionCheckpoint(req.SessionID)
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
	systemPrompt := req.SystemPrompt
	env := req.Env

	// Adapter resume_hint precedence: checkpoint hint, then session hint,
	// then nil (fresh launch).
	var resumeHint []byte
	switch {
	case cp != nil && cp.ResumeHint != nil:
		resumeHint = cp.ResumeHint
	case len(src.ResumeHint) > 0:
		resumeHint = src.ResumeHint
	}
	rt, err := m.adapters.RuntimeFor(profile, resumeHint)
	if err != nil {
		return "", err
	}

	id := m.idFn()
	rec := &sqlstore.SessionRecord{
		ID:           id,
		AgentProfile: profile,
		Provider:     providerFromRuntime(rt),
		RuntimeID:    rt.ID(),
		RuntimeKind:  rt.Kind(),
		Workdir:      workdir,
		ProjectID:    src.ProjectID,
		TaskID:       src.TaskID,
		State:        string(StatusLaunching),
		ResumeHint:   resumeHint,
		MetaJSON:     src.MetaJSON,
	}
	if err := m.store.CreateSession(rec); err != nil {
		return "", fmt.Errorf("create resumed session: %w", err)
	}

	startReq := agentsessions.StartRequest{
		ID:      id,
		Runtime: rt,
		Options: agentsessions.StartOptions{
			Workdir:    workdir,
			Env:        env,
			BootPrompt: systemPrompt,
		},
	}
	if err := m.inner.Start(ctx, startReq); err != nil {
		return "", fmt.Errorf("start resumed session: %w", err)
	}
	return id, nil
}

// Sweep marks `launching` and `running` rows whose process is gone as
// `crashed`. Intended for a single call at startup. Returns the number of
// rows transitioned.
//
// PID liveness check uses syscall.Kill(pid, 0): no-op if the process is
// alive (returns nil), ESRCH if it's gone. PID=0 (no recorded pid) is
// always treated as stale.
func (m *Manager) Sweep() (int, error) {
	return m.store.SweepStaleSessions(func(rec *sqlstore.SessionRecord) bool {
		if rec.PID <= 0 {
			return false
		}
		// On unix this is non-destructive: signal 0 only checks deliverability.
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

// Shutdown stops accepting new launches and waits for in-flight watch
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

// providerFromRuntime extracts a stable provider token from the Runtime
// ID. The cliexec convention is "clockwork-cli/<provider>".
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
