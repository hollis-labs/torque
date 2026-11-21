package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/tetherclient"
)

// G6 (CW-20261121-0148): an assignment expires when its holder is gone.
//
// Torque records who holds a task where it knows: a unit team run it dispatched
// (team_run_id, lease = the unit deadline), a direct run bound to a Tether session,
// and a workstream claim (lease). metadata.owner_urn is projected as a plain
// assignment (source=owner) when nothing stronger exists. A reconcile loop
// releases assignments whose task finished, and expires those whose session ended,
// whose team run failed, or whose lease lapsed: the row keeps the stale holder
// with state=expired, a comment is written on the task, and Ops gets a status
// post (kind assignment_expired).
//
// The typed task record exposes it as `assignment` (surface#68 contract).

// TaskAssignment is one task's current or last assignment.
type TaskAssignment struct {
	TaskID          string `json:"-"`
	AssigneeURN     string `json:"assignee_urn"`
	AssigneeName    string `json:"assignee_name"`
	TetherSessionID string `json:"tether_session_id"`
	TeamRunID       string `json:"team_run_id"`
	ClaimID         string `json:"claim_id"`
	Source          string `json:"source"`
	State           string `json:"state"`
	AssignedAt      string `json:"assigned_at"`
	LeaseExpiresAt  string `json:"lease_expires_at"`
	EndedAt         string `json:"ended_at"`
	EndReason       string `json:"end_reason"`
}

// View is the typed-record shape: empty strings become null.
func (a *TaskAssignment) View() map[string]any {
	if a == nil {
		return nil
	}
	n := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	return map[string]any{
		"assignee_urn": a.AssigneeURN, "assignee_name": n(a.AssigneeName),
		"tether_session_id": n(a.TetherSessionID), "team_run_id": n(a.TeamRunID), "claim_id": n(a.ClaimID),
		"source": a.Source, "state": a.State, "assigned_at": a.AssignedAt,
		"lease_expires_at": n(a.LeaseExpiresAt), "ended_at": n(a.EndedAt), "end_reason": n(a.EndReason),
	}
}

// SessionLookup is the Tether call the reconciler needs (tetherclient.Client).
type SessionLookup interface {
	GetSession(ctx context.Context, id string) (*tetherclient.SessionState, error)
}

// AssignmentService records, projects and expires task assignments.
type AssignmentService struct {
	store    *sqlstore.Store
	sessions SessionLookup // nil: session liveness is not checked
	// status, when set, posts assignment_expired to the status stream (DispatchSync).
	status func(ctx context.Context, t *sqlstore.TaskRecord, a *TaskAssignment, text string) error
	now    func() time.Time
	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewAssignmentService(store *sqlstore.Store) *AssignmentService {
	return &AssignmentService{store: store, now: time.Now}
}

// SetSessions installs the Tether session lookup used to detect dead holders.
func (s *AssignmentService) SetSessions(l SessionLookup) { s.sessions = l }

// SetStatusSink installs the status-stream writer for expiry events.
func (s *AssignmentService) SetStatusSink(fn func(ctx context.Context, t *sqlstore.TaskRecord, a *TaskAssignment, text string) error) {
	s.status = fn
}

const assignmentCols = `task_id,assignee_urn,assignee_name,tether_session_id,team_run_id,claim_id,source,state,assigned_at,lease_expires_at,ended_at,end_reason`

func scanAssignment(row interface{ Scan(...any) error }) (*TaskAssignment, error) {
	var a TaskAssignment
	if err := row.Scan(&a.TaskID, &a.AssigneeURN, &a.AssigneeName, &a.TetherSessionID, &a.TeamRunID, &a.ClaimID, &a.Source, &a.State, &a.AssignedAt, &a.LeaseExpiresAt, &a.EndedAt, &a.EndReason); err != nil {
		return nil, err
	}
	return &a, nil
}

// Get returns the task's assignment: the stored row, else the owner_urn
// projection, else nil.
func (s *AssignmentService) Get(t *sqlstore.TaskRecord) (*TaskAssignment, error) {
	a, err := scanAssignment(s.store.ReadDB().QueryRow(`SELECT `+assignmentCols+` FROM task_assignments WHERE task_id=?`, t.ID))
	if err == nil {
		return a, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return ownerProjection(t), nil
}

func ownerProjection(t *sqlstore.TaskRecord) *TaskAssignment {
	m := taskMetadata(t)
	urn, _ := m["owner_urn"].(string)
	urn = strings.TrimSpace(urn)
	if urn == "" {
		return nil
	}
	name, _ := m["owner_name"].(string)
	state := "active"
	end := ""
	if isFinished(t.Status) {
		state, end = "released", "done"
	}
	return &TaskAssignment{TaskID: t.ID, AssigneeURN: urn, AssigneeName: name, Source: "owner", State: state, AssignedAt: t.CreatedAt.UTC().Format(time.RFC3339), EndReason: end}
}

func isFinished(status string) bool {
	switch status {
	case "done", "review", "cancelled", "abandoned", "archived":
		return true
	}
	return false
}

// upsertActive records a new active assignment unless the same holder already
// has one (idempotent per reconcile pass).
func (s *AssignmentService) upsertActive(ctx context.Context, a TaskAssignment) error {
	w, err := s.store.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()
	_, err = w.Exec(`INSERT INTO task_assignments(`+assignmentCols+`) VALUES (?,?,?,?,?,?,?,'active',?,?,'','')
 ON CONFLICT(task_id) DO UPDATE SET assignee_urn=excluded.assignee_urn,assignee_name=excluded.assignee_name,
 tether_session_id=excluded.tether_session_id,team_run_id=excluded.team_run_id,claim_id=excluded.claim_id,source=excluded.source,
 state='active',assigned_at=excluded.assigned_at,lease_expires_at=excluded.lease_expires_at,ended_at='',end_reason=''
 WHERE NOT (task_assignments.source=excluded.source AND task_assignments.tether_session_id=excluded.tether_session_id
   AND task_assignments.team_run_id=excluded.team_run_id AND task_assignments.claim_id=excluded.claim_id)`,
		a.TaskID, a.AssigneeURN, a.AssigneeName, a.TetherSessionID, a.TeamRunID, a.ClaimID, a.Source, a.AssignedAt, a.LeaseExpiresAt)
	if err != nil {
		return err
	}
	// Keep the lease current for the same holder (claims renew).
	if _, err = w.Exec(`UPDATE task_assignments SET lease_expires_at=? WHERE task_id=? AND state='active' AND claim_id=? AND team_run_id=? AND tether_session_id=?`, a.LeaseExpiresAt, a.TaskID, a.ClaimID, a.TeamRunID, a.TetherSessionID); err != nil {
		return err
	}
	return w.Commit()
}

// end moves an active assignment to expired or released. It reports whether it
// changed anything (so side effects run once).
func (s *AssignmentService) end(ctx context.Context, taskID, state, reason string) (bool, error) {
	w, err := s.store.BeginWriteTx(ctx)
	if err != nil {
		return false, err
	}
	defer w.Rollback()
	res, err := w.Exec(`UPDATE task_assignments SET state=?,end_reason=?,ended_at=? WHERE task_id=? AND state='active'`, state, reason, s.now().UTC().Format(time.RFC3339), taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, w.Commit()
}

// Start runs Reconcile every interval until Stop.
func (s *AssignmentService) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
					log.Printf("[assignments] %v", err)
				}
			}
		}
	}()
}

func (s *AssignmentService) Stop() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// Reconcile records holders Torque knows about, then releases or expires
// assignments whose holder is finished or gone.
func (s *AssignmentService) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	if err := s.recordUnits(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.recordRuns(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.recordClaims(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.sweep(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// recordUnits: a live unit team run holds its task until the unit deadline.
func (s *AssignmentService) recordUnits(ctx context.Context) error {
	rows, err := s.store.ReadDB().QueryContext(ctx, `SELECT id,
 COALESCE(json_extract(metadata,'$.unit_run.run_id'),''), COALESCE(json_extract(metadata,'$.unit_run.started_at'),''),
 COALESCE(json_extract(metadata,'$.unit_run.deadline'),'')
 FROM tasks WHERE json_valid(metadata) AND json_extract(metadata,'$.unit_run.state') IN ('forming','active','unconfirmed')
 AND status IN ('todo','queued','doing','blocked')`)
	if err != nil {
		return err
	}
	var batch []TaskAssignment
	for rows.Next() {
		var id, run, started, deadline string
		if err = rows.Scan(&id, &run, &started, &deadline); err != nil {
			rows.Close()
			return err
		}
		if run == "" {
			continue
		}
		lease := ""
		if deadline != "" {
			lease = normTime(deadline, s.now)
		}
		batch = append(batch, TaskAssignment{TaskID: id, AssigneeURN: "msg://agent/agent-mux/team-orchestrator#" + run, TeamRunID: run, Source: "dispatch", AssignedAt: normTime(started, s.now), LeaseExpiresAt: lease})
	}
	rows.Close()
	for _, a := range batch {
		if err := s.upsertActive(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// recordRuns: a running executor run bound to a Tether session holds its task.
func (s *AssignmentService) recordRuns(ctx context.Context) error {
	rows, err := s.store.ReadDB().QueryContext(ctx, `SELECT r.task_id, r.tether_session_id, r.started_at FROM runs r JOIN tasks t ON t.id=r.task_id
 WHERE r.status='running' AND COALESCE(r.tether_session_id,'')<>'' AND t.status IN ('doing','blocked')`)
	if err != nil {
		return err
	}
	var batch []TaskAssignment
	for rows.Next() {
		var task, session string
		var started sql.NullString
		if err = rows.Scan(&task, &session, &started); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, TaskAssignment{TaskID: task, AssigneeURN: "msg://session/local/" + session, TetherSessionID: session, Source: "dispatch", AssignedAt: normTime(started.String, s.now)})
	}
	rows.Close()
	for _, a := range batch {
		if err := s.upsertActive(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// recordClaims: an active workstream claim holds its task until its lease.
func (s *AssignmentService) recordClaims(ctx context.Context) error {
	rows, err := s.store.ReadDB().QueryContext(ctx, `SELECT task_id,id,team_run_id,actor,lease_expires_at,created_at FROM workstream_claims WHERE status='active'`)
	if err != nil {
		return err
	}
	var batch []TaskAssignment
	for rows.Next() {
		var task, id, run, actor string
		var lease, created sql.NullString
		if err = rows.Scan(&task, &id, &run, &actor, &lease, &created); err != nil {
			rows.Close()
			return err
		}
		urn := actor
		if !strings.HasPrefix(urn, "msg://") {
			urn = "msg://agent/agent-mux/team-orchestrator#" + run
		}
		a := TaskAssignment{TaskID: task, AssigneeURN: urn, TeamRunID: run, ClaimID: id, Source: "claim", AssignedAt: normTime(created.String, s.now)}
		if lease.String != "" {
			a.LeaseExpiresAt = normTime(lease.String, s.now)
		}
		batch = append(batch, a)
	}
	rows.Close()
	for _, a := range batch {
		if err := s.upsertActive(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// sweep releases finished work and expires dead holders.
func (s *AssignmentService) sweep(ctx context.Context) error {
	rows, err := s.store.ReadDB().QueryContext(ctx, `SELECT `+assignmentCols+` FROM task_assignments WHERE state='active'`)
	if err != nil {
		return err
	}
	var active []*TaskAssignment
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			rows.Close()
			return err
		}
		active = append(active, a)
	}
	rows.Close()
	now := s.now().UTC()
	var errs []error
	for _, a := range active {
		t, err := s.store.GetTask(a.TaskID)
		if err != nil {
			continue
		}
		if isFinished(t.Status) {
			if _, err := s.end(ctx, a.TaskID, "released", "done"); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		reason := ""
		switch {
		case a.LeaseExpiresAt != "" && leaseLapsed(a.LeaseExpiresAt, now):
			reason = "lease_lapsed"
		case a.TeamRunID != "" && a.Source == "dispatch":
			m := taskMetadata(t)
			if u, ok := m["unit_run"].(map[string]any); ok {
				run, _ := u["run_id"].(string)
				state, _ := u["state"].(string)
				if run == a.TeamRunID {
					switch state {
					case UnitFailed, UnitBlocked:
						reason = "session_ended"
					case UnitDone:
						reason = "released"
					}
				} else if run != "" {
					reason = "released" // a newer attempt replaced this run; it records its own row
				}
			}
		case a.TeamRunID != "" && a.Source == "claim":
			var status string
			if e := s.store.ReadDB().QueryRowContext(ctx, `SELECT status FROM workstream_claims WHERE id=?`, a.ClaimID).Scan(&status); e == nil && status != "active" {
				reason = "released"
			}
		case a.TetherSessionID != "" && s.sessions != nil:
			sess, e := s.sessions.GetSession(ctx, a.TetherSessionID)
			if tetherclient.IsNotFound(e) {
				reason = "session_ended"
			} else if e == nil && sessionEnded(sess.State) {
				reason = "session_ended"
			}
		}
		if reason == "" {
			continue
		}
		state := "expired"
		if reason == "released" {
			state = "released"
		}
		changed, err := s.end(ctx, a.TaskID, state, reason)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if changed && state == "expired" {
			a.State, a.EndReason = state, reason
			s.announceExpiry(ctx, t, a, reason)
		}
	}
	return errors.Join(errs...)
}

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02T15:04:05Z", "2006-01-02 15:04:05"}

func parseLooseTime(v string) (time.Time, bool) {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, strings.TrimSpace(v)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// normTime renders a stored timestamp as RFC3339 UTC (fallback: now).
func normTime(v string, now func() time.Time) string {
	if t, ok := parseLooseTime(v); ok {
		return t.UTC().Format(time.RFC3339)
	}
	return now().UTC().Format(time.RFC3339)
}

func leaseLapsed(lease string, now time.Time) bool {
	t, ok := parseLooseTime(lease)
	return ok && now.After(t)
}

func sessionEnded(state string) bool {
	switch state {
	case tetherclient.StateCompleted, tetherclient.StateFailed, tetherclient.StateKilled, "exited", "stopped", "ended", "lost":
		return true
	}
	return false
}

// announceExpiry writes the task comment and the Ops status post, once per
// expiry (the caller only calls it when the row changed).
func (s *AssignmentService) announceExpiry(ctx context.Context, t *sqlstore.TaskRecord, a *TaskAssignment, reason string) {
	why := map[string]string{"session_ended": "its session ended", "lease_lapsed": "its lease lapsed"}[reason]
	holder := a.AssigneeURN
	if a.TeamRunID != "" {
		holder += " (team run " + a.TeamRunID + ")"
	} else if a.TetherSessionID != "" {
		holder += " (session " + a.TetherSessionID + ")"
	}
	text := fmt.Sprintf("Assignment expired: %s held this task and %s. The task is %s with no live holder.", holder, why, t.Status)
	if err := s.store.AddComment(&sqlstore.CommentRecord{EntityType: "task", EntityID: t.ID, Author: "torque-assignments", Content: text}); err != nil {
		log.Printf("[assignments] comment %s: %v", t.ID, err)
	}
	if s.status == nil {
		return
	}
	if err := s.status(ctx, t, a, text); err != nil {
		log.Printf("[assignments] status post %s: %v", t.ID, err)
	}
}

// RecordDoing records the gateway-vouched caller who moved a task to doing
// (source=doing). Without a caller in ctx (stdio, direct API, scheduler) it does
// nothing: dispatch, claims and owner_urn cover those. A stronger holder that is
// already active (a unit run or claim) is not replaced.
func (s *AssignmentService) RecordDoing(ctx context.Context, taskID string) {
	c, ok := CallerFrom(ctx)
	if !ok || (c.AgentURN == "" && c.SessionID == "") {
		return
	}
	var source string
	err := s.store.ReadDB().QueryRowContext(ctx, `SELECT source FROM task_assignments WHERE task_id=? AND state='active'`, taskID).Scan(&source)
	if err == nil && (source == "dispatch" || source == "claim") {
		return
	}
	urn := c.AgentURN
	if urn == "" {
		urn = "msg://session/local/" + c.SessionID
	}
	a := TaskAssignment{TaskID: taskID, AssigneeURN: urn, TetherSessionID: c.SessionID, Source: "doing", AssignedAt: s.now().UTC().Format(time.RFC3339)}
	if err := s.upsertActive(ctx, a); err != nil {
		log.Printf("[assignments] record doing %s: %v", taskID, err)
	}
}
