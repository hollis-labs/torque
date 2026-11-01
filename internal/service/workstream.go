package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/tetherclient"
)

// Claim outcomes. They are the dispatch-policy outcomes of spec CW-20261003-0154
// that apply to claim-next; AwaitDecision is not produced yet.
const (
	ClaimDispatch = "dispatch"
	ClaimDefer    = "defer"
	ClaimRefuse   = "refuse"
)

// Defer reasons.
const (
	DeferPaused        = "paused"
	DeferProjectCap    = "project_cap"
	DeferNoClearedWork = "no_cleared_work"

	// Tether-reported capacity. capacity_unknown fails closed: with a Tether
	// reader configured, a claim that names an environment is not handed out
	// on a capacity read that failed.
	DeferEnvironmentCap    = "environment_cap"
	DeferEnvironmentPaused = "environment_paused"
	DeferCapacityUnknown   = "capacity_unknown"
)

// Default policy. Zero never means unlimited.
const (
	DefaultProjectClaimCap = 1
	DefaultClaimLeaseTTL   = 15 * time.Minute
)

// WorkstreamService hands cleared workstreams (parent tasks with children) to
// team runs on request. Torque owns the queue and the go/pause switch; the
// team run, its sessions and its placement belong to Tether. It never launches.
type WorkstreamService struct {
	store *sqlstore.Store
	task  *TaskService

	goFn       func() bool
	projectCap int
	leaseTTL   time.Duration
	tether     TetherReader
	// tetherEnv, when set (TORQUE_TETHER_ENVIRONMENT_ID), is the environment_id
	// /capacity must report. Empty means the reported id is informational.
	tetherEnv string
	// runLookupWarned makes the "/teams/runs unavailable" log fire once.
	runLookupWarned atomic.Bool
}

// TetherReader is the read-only slice of Tether's API the claim path uses:
// per-environment capacity and team-run state. nil (the default) means Tether
// is not consulted: environment_id is recorded on the claim and not enforced,
// and expired leases are surfaced but never requeued.
type TetherReader interface {
	Capacity(ctx context.Context) (*tetherclient.Capacity, error)
	Run(ctx context.Context, runID string) (*tetherclient.RunState, error)
}

// SetTether installs the Tether reader. A nil *tetherclient.Client must be
// passed as an untyped nil so the interface stays nil.
func (s *WorkstreamService) SetTether(t TetherReader) { s.tether = t }

// SetTetherEnvironment makes the capacity check require Tether's /capacity to
// report this environment_id. Unset (the default), the reported id is
// informational: Tether answers for its own environment (e.g. "local"), which
// need not match the id a team run names in its claim.
func (s *WorkstreamService) SetTetherEnvironment(id string) { s.tetherEnv = strings.TrimSpace(id) }

// SetGo installs the go/pause source, normally the scheduler's enabled flag.
// Without one, claims defer as paused: a process that cannot see the switch
// must not hand out work.
func (s *WorkstreamService) SetGo(f func() bool) { s.goFn = f }

// SetPolicy overrides the per-project concurrent-claim cap and the lease TTL.
// Values <= 0 keep the defaults.
func (s *WorkstreamService) SetPolicy(projectCap int, leaseTTL time.Duration) {
	if projectCap > 0 {
		s.projectCap = projectCap
	}
	if leaseTTL > 0 {
		s.leaseTTL = leaseTTL
	}
}

// ClaimInput identifies the caller. TeamRunID is the Tether run id (a team
// definition id alone does not identify a formation). IdempotencyKey makes a
// retried claim return the same lease.
type ClaimInput struct {
	TeamID         string
	TeamRunID      string
	Actor          string
	EnvironmentID  string
	ProjectScope   []string
	IdempotencyKey string
}

// ClaimResult is one of Dispatch, Defer or Refuse.
type ClaimResult struct {
	Outcome        string    `json:"outcome"`
	ClaimID        string    `json:"claim_id,omitempty"`
	TaskID         string    `json:"task_id,omitempty"`
	LeaseExpiresAt time.Time `json:"lease_expires_at,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	Diagnostics    string    `json:"diagnostics,omitempty"`
}

func (s *WorkstreamService) cap() int {
	if s.projectCap > 0 {
		return s.projectCap
	}
	return DefaultProjectClaimCap
}

func (s *WorkstreamService) ttl() time.Duration {
	if s.leaseTTL > 0 {
		return s.leaseTTL
	}
	return DefaultClaimLeaseTTL
}

// ClaimNext applies go/pause, the per-project cap and eligibility, and records
// the claim, in one write transaction. The task moves to doing in the same
// transaction so no other path treats it as queued.
func (s *WorkstreamService) ClaimNext(ctx context.Context, in ClaimInput) (*ClaimResult, error) {
	if strings.TrimSpace(in.TeamRunID) == "" {
		return &ClaimResult{Outcome: ClaimRefuse, Reason: "invalid", Diagnostics: "team_run_id is required: claims are keyed on the Tether run id"}, nil
	}
	// Capacity is read before the write transaction opens so a slow Tether
	// call never holds the writer. A retried claim still returns its existing
	// lease: the idempotency lookup comes first.
	if s.tether != nil && in.EnvironmentID != "" {
		if in.IdempotencyKey != "" {
			if prev, err := s.store.FindWorkstreamClaimByKey(in.TeamRunID, in.IdempotencyKey); err != nil {
				return nil, err
			} else if prev != nil {
				return &ClaimResult{Outcome: ClaimDispatch, ClaimID: prev.ID, TaskID: prev.TaskID, LeaseExpiresAt: prev.LeaseExpiresAt}, nil
			}
		}
		if s.goFn != nil && s.goFn() {
			if d := s.capacityDefer(ctx); d != nil {
				return d, nil
			}
		}
	}

	wtx, err := s.store.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer wtx.Rollback()

	if in.IdempotencyKey != "" {
		prev, err := wtx.FindWorkstreamClaimByKey(in.TeamRunID, in.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if prev != nil {
			return &ClaimResult{Outcome: ClaimDispatch, ClaimID: prev.ID, TaskID: prev.TaskID, LeaseExpiresAt: prev.LeaseExpiresAt}, nil
		}
	}
	if s.goFn == nil || !s.goFn() {
		return &ClaimResult{Outcome: ClaimDefer, Reason: DeferPaused}, nil
	}

	atCap, err := s.projectsAtCap(wtx)
	if err != nil {
		return nil, err
	}
	next, err := wtx.NextClaimableWorkstream(in.ProjectScope, atCap)
	if err != nil {
		return nil, err
	}
	if next == nil {
		reason := DeferNoClearedWork
		if len(atCap) > 0 {
			if blocked, err := wtx.NextClaimableWorkstream(in.ProjectScope, nil); err == nil && blocked != nil {
				reason = DeferProjectCap
			}
		}
		return &ClaimResult{Outcome: ClaimDefer, Reason: reason}, nil
	}

	c := &sqlstore.WorkstreamClaim{
		ID: newClaimID(), TaskID: next.TaskID, ProjectID: next.ProjectID,
		TeamID: in.TeamID, TeamRunID: in.TeamRunID, Actor: in.Actor,
		EnvironmentID: in.EnvironmentID, IdempotencyKey: in.IdempotencyKey,
		LeaseExpiresAt: time.Now().UTC().Add(s.ttl()).Truncate(time.Second),
	}
	if err := wtx.InsertWorkstreamClaim(c); err != nil {
		return nil, err
	}
	if err := wtx.TransitionTask(next.TaskID, "doing"); err != nil {
		return nil, err
	}
	if err := wtx.Commit(); err != nil {
		return nil, err
	}
	s.task.notifyTransition(ctx, next.TaskID, "todo", "doing")
	return &ClaimResult{Outcome: ClaimDispatch, ClaimID: c.ID, TaskID: c.TaskID, LeaseExpiresAt: c.LeaseExpiresAt}, nil
}

// capacityDefer returns a Defer when Tether says the environment cannot take
// another session, or when it cannot be read, else nil.
func (s *WorkstreamService) capacityDefer(ctx context.Context) *ClaimResult {
	c, err := s.tether.Capacity(ctx)
	if err != nil {
		log.Printf("[workstream] capacity read failed: %v", err)
		return &ClaimResult{Outcome: ClaimDefer, Reason: DeferCapacityUnknown, Diagnostics: "Tether capacity read failed; not handing out work blind"}
	}
	if s.tetherEnv != "" && c.EnvironmentID != "" && c.EnvironmentID != s.tetherEnv {
		return &ClaimResult{Outcome: ClaimDefer, Reason: DeferCapacityUnknown, Diagnostics: "Tether reported capacity for a different environment"}
	}
	if strings.EqualFold(c.Admission, "paused") {
		return &ClaimResult{Outcome: ClaimDefer, Reason: DeferEnvironmentPaused, Diagnostics: c.Reason}
	}
	if c.MaxSessions > 0 && c.ActiveSessions >= c.MaxSessions {
		return &ClaimResult{Outcome: ClaimDefer, Reason: DeferEnvironmentCap}
	}
	return nil
}

// projectsAtCap returns the projects whose active claims reach the cap.
func (s *WorkstreamService) projectsAtCap(w *sqlstore.WriteTx) (map[string]bool, error) {
	projects, err := w.ProjectsWithActiveClaims()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, p := range projects {
		n, err := w.CountActiveWorkstreamClaims(p)
		if err != nil {
			return nil, err
		}
		if n >= s.cap() {
			out[p] = true
		}
	}
	return out, nil
}

// Heartbeat extends an active claim's lease. The caller (Tether) renews while
// the team run is alive; Torque never reclaims on silence alone.
func (s *WorkstreamService) Heartbeat(ctx context.Context, claimID string) (time.Time, error) {
	wtx, err := s.store.BeginWriteTx(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer wtx.Rollback()
	c, err := wtx.GetWorkstreamClaim(claimID)
	if err != nil {
		return time.Time{}, err
	}
	if c.Status != "active" {
		return time.Time{}, &TransitionError{From: c.Status, To: "active", Message: fmt.Sprintf("claim %s is already released (%s)", claimID, c.Outcome)}
	}
	until := time.Now().UTC().Add(s.ttl()).Truncate(time.Second)
	if err := wtx.RenewWorkstreamClaim(claimID, until); err != nil {
		return time.Time{}, err
	}
	return until, wtx.Commit()
}

// Release outcomes.
const (
	ReleaseCompleted = "completed"
	ReleaseFailed    = "failed"
	ReleaseCanceled  = "canceled"
	ReleaseHandoff   = "handoff"
)

// ReleaseResult reports where the workstream task ended up.
type ReleaseResult struct {
	ClaimID    string `json:"claim_id"`
	TaskID     string `json:"task_id"`
	TaskStatus string `json:"task_status"`
	Reason     string `json:"reason,omitempty"`
}

// Release ends a claim. completed asks for done and therefore passes the
// landing gate: missing evidence leaves the task blocked and escalated, and
// the release still succeeds, because the team's work did end. failed blocks
// the task; canceled and handoff return it to the queue.
func (s *WorkstreamService) Release(ctx context.Context, claimID, outcome, resultRefs string) (*ReleaseResult, error) {
	switch outcome {
	case ReleaseCompleted, ReleaseFailed, ReleaseCanceled, ReleaseHandoff:
	default:
		return nil, &ValidationError{Field: "outcome", Message: "outcome must be one of: completed, failed, canceled, handoff"}
	}
	wtx, err := s.store.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer wtx.Rollback()
	c, err := wtx.GetWorkstreamClaim(claimID)
	if err != nil {
		return nil, err
	}
	if c.Status != "active" {
		return nil, &TransitionError{From: c.Status, To: "released", Message: fmt.Sprintf("claim %s is already released (%s)", claimID, c.Outcome)}
	}
	if err := wtx.ReleaseWorkstreamClaim(claimID, outcome, resultRefs); err != nil {
		return nil, err
	}
	switch outcome {
	case ReleaseFailed:
		err = wtx.TransitionTaskWithReason(c.TaskID, "blocked", "team run failed; claim "+claimID)
	case ReleaseCanceled, ReleaseHandoff:
		err = wtx.TransitionTask(c.TaskID, "todo")
	}
	if err != nil {
		return nil, err
	}
	if err := wtx.Commit(); err != nil {
		return nil, err
	}

	res := &ReleaseResult{ClaimID: claimID, TaskID: c.TaskID}
	switch outcome {
	case ReleaseCompleted:
		if terr := s.task.Transition(ctx, c.TaskID, "done"); terr != nil {
			res.Reason = terr.Error()
		}
	case ReleaseFailed:
		s.task.notifyTransition(ctx, c.TaskID, "doing", "blocked")
	default:
		s.task.notifyTransition(ctx, c.TaskID, "doing", "todo")
	}
	t, err := s.task.Get(c.TaskID)
	if err != nil {
		return nil, err
	}
	res.TaskStatus = t.Status
	return res, nil
}

func newClaimID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("clm_%d", time.Now().UnixNano())
	}
	return "clm_" + hex.EncodeToString(b[:])
}

// Sweep actions.
const (
	SweepRequeued   = "requeued"   // Tether says the run ended; task back to todo
	SweepRunActive  = "run_active" // lease expired but the run is still alive; left alone
	SweepUnverified = "unverified" // run state could not be read (incl. 404), or no Tether reader; left alone
)

// ExpiredClaim reports one expired lease and what the sweep did.
type ExpiredClaim struct {
	ClaimID        string    `json:"claim_id"`
	TaskID         string    `json:"task_id"`
	TeamRunID      string    `json:"team_run_id"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	Action         string    `json:"action"`
	Detail         string    `json:"detail,omitempty"`
}

// SweepExpiredClaims handles claims whose lease has run out. Silence is not
// death: a claim is requeued only when Tether's run-state read says the run
// ended or no longer exists. Without a Tether reader, or when the read fails,
// the claim is reported and left alone for review.
func (s *WorkstreamService) SweepExpiredClaims(ctx context.Context) ([]ExpiredClaim, error) {
	active, err := s.store.ListWorkstreamClaims("active")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var out []ExpiredClaim
	for _, c := range active {
		if !c.LeaseExpiresAt.Before(now) {
			continue
		}
		e := ExpiredClaim{ClaimID: c.ID, TaskID: c.TaskID, TeamRunID: c.TeamRunID, LeaseExpiresAt: c.LeaseExpiresAt, Action: SweepUnverified}
		if s.tether == nil {
			e.Detail = "no Tether reader configured; cannot confirm the run ended"
			out = append(out, e)
			continue
		}
		run, err := s.tether.Run(ctx, c.TeamRunID)
		switch {
		case errors.Is(err, tetherclient.ErrRunNotFound):
			// GET /teams/runs/{id} is unverified against Tether: a 404 may mean
			// the endpoint is missing, so it is not proof the run ended. Leave
			// the claim alone and say so once.
			if s.runLookupWarned.CompareAndSwap(false, true) {
				log.Printf("[workstream] Tether returned 404 for /teams/runs/%s; the endpoint is unverified, so expired leases are left unverified rather than requeued", c.TeamRunID)
			}
			e.Detail = "Tether returned 404 for the run (endpoint unverified); left alone"
			out = append(out, e)
			continue
		case err != nil:
			e.Detail = "run state unreadable: " + err.Error()
			out = append(out, e)
			continue
		case !run.Ended():
			e.Action, e.Detail = SweepRunActive, "run status "+run.Status
			out = append(out, e)
			continue
		default:
			e.Detail = "run ended: " + run.Status
		}
		if _, err := s.Release(ctx, c.ID, ReleaseHandoff, ""); err != nil {
			e.Detail += "; requeue failed: " + err.Error()
			out = append(out, e)
			continue
		}
		e.Action = SweepRequeued
		out = append(out, e)
	}
	return out, nil
}

// ListClaims returns claims by status for operators; expired is derived.
func (s *WorkstreamService) ListClaims(status string) ([]sqlstore.WorkstreamClaim, error) {
	return s.store.ListWorkstreamClaims(status)
}
