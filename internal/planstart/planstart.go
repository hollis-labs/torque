// Package planstart implements the V0 trigger that boots an Orchestrator
// session for a kind=plan task (CW-20260503-0017, S2.1). HTTP
// (/api/v1/plans/{id}/start) and MCP (clockwork_plan_start) both call
// Start; the GUI's Execute Plan button hits the HTTP route.
//
// V0 wires the trigger as a hardcoded MCP tool + HTTP route. Refactor
// onto Plans v2 lifecycle hooks (phase_start / plan_complete) is
// deferred to CW-20260417-0135.
package planstart

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/orchestrator"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/sessionmgr"
)

// Sentinel errors for the trigger. Handlers map them to HTTP status:
//   - ErrPlanNotFound      → 422 (plan id is not a kind=plan task)
//   - ErrPlanWrongStatus   → 422 (plan in done/blocked/abandoned)
//   - ErrAlreadyOrchestrating → 409 (an orchestrator session is live)
//   - ErrWorkdirRequired   → 422 (Options.Workdir empty AND plan.WorkingDir empty)
//   - ErrSessionMgrMissing → 503 (substrate not wired into this host)
var (
	ErrPlanNotFound         = errors.New("planstart: plan not found or not a kind=plan task")
	ErrPlanWrongStatus      = errors.New("planstart: plan must be in todo or review status to start")
	ErrAlreadyOrchestrating = errors.New("planstart: orchestrator session is already running for this plan")
	ErrWorkdirRequired      = errors.New("planstart: workdir required (Options.Workdir empty and plan has no WorkingDir)")
	ErrSessionMgrMissing    = errors.New("planstart: session manager not configured in this host")
)

// Options configures Start. Workdir defaults to the plan task's
// WorkingDir column when empty; explicit override wins.
type Options struct {
	// Workdir for the orchestrator session. When empty, uses the
	// plan task's WorkingDir column. Required (one or the other).
	Workdir string

	// Env passes additional environment to the orchestrator session.
	// Tests pass nil.
	Env []string
}

// Result is what Start returns on success. It mirrors the documented
// HTTP/MCP response: session_id + plan_id + an RFC3339 started_at
// stamp the GUI/CLI shows in the "Execute Plan" toast.
type Result struct {
	SessionID string    `json:"session_id"`
	PlanID    string    `json:"plan_id"`
	StartedAt time.Time `json:"started_at"`
}

// Store is the narrowed dependency surface Start uses. Defined as an
// interface so tests can substitute an in-memory fake without dragging
// the full sqlstore.Store into the test harness. Production passes
// *sqlstore.Store directly — it satisfies this contract.
type Store interface {
	GetTask(id string) (*sqlstore.TaskRecord, error)
	UpdateTask(id string, u sqlstore.TaskUpdate) error
	TransitionTask(id, newStatus string) error
	GetSession(id string) (*sqlstore.SessionRecord, error)
}

// SessionManager is the narrowed sessionmgr surface. The real
// *sessionmgr.Manager implements it; tests pass a stub.
type SessionManager interface {
	Launch(ctx context.Context, req sessionmgr.LaunchRequest) (string, error)
}

// Start validates a plan, idempotency-checks any existing orchestrator
// session, boots a fresh one when needed, transitions the plan to
// `doing`, and stamps metadata.plan.orchestrator_session_id. Returns
// the orchestrator session id on success.
//
// Idempotency: when metadata.plan.orchestrator_session_id is already
// set AND the named session is still live (state launching|running),
// Start returns ErrAlreadyOrchestrating wrapping a Result so callers
// can surface the existing session_id (HTTP 409).
func Start(ctx context.Context, store Store, mgr SessionManager, planID string, opts Options) (*Result, error) {
	if mgr == nil {
		return nil, ErrSessionMgrMissing
	}
	plan, err := store.GetTask(planID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPlanNotFound, err)
	}
	if plan.Kind != "plan" {
		return nil, fmt.Errorf("%w: kind=%q", ErrPlanNotFound, plan.Kind)
	}
	if !startableStatus(plan.Status) {
		return nil, fmt.Errorf("%w: status=%q", ErrPlanWrongStatus, plan.Status)
	}

	// Idempotency: existing session still alive?
	if existing, ok := readOrchestratorSessionID(plan); ok && existing != "" {
		if rec, err := store.GetSession(existing); err == nil && !sessionTerminal(rec.State) {
			return &Result{
				SessionID: existing,
				PlanID:    planID,
				StartedAt: rec.CreatedAt,
			}, fmt.Errorf("%w: session=%s", ErrAlreadyOrchestrating, existing)
		}
		// Stale or missing session row — drop the metadata and proceed
		// with a fresh launch. Avoids leaving the plan stuck on a
		// crashed/orphaned session id forever.
	}

	workdir := opts.Workdir
	if workdir == "" {
		workdir = plan.WorkingDir
	}
	if workdir == "" {
		return nil, ErrWorkdirRequired
	}

	launchReq, err := orchestrator.BuildLaunchRequest(orchestrator.LaunchOptions{
		PlanID:    planID,
		Workdir:   workdir,
		ProjectID: nullStr(plan.ProjectID),
		TaskID:    planID,
		Env:       opts.Env,
	})
	if err != nil {
		return nil, fmt.Errorf("planstart: build launch request: %w", err)
	}

	sessionID, err := mgr.Launch(ctx, launchReq)
	if err != nil {
		return nil, fmt.Errorf("planstart: launch orchestrator: %w", err)
	}

	// Stamp orchestrator_session_id into metadata.plan and transition
	// plan → doing. Both writes are best-effort cleanup if the second
	// fails: the session is already running and visible via
	// clockwork_session_list; the plan's status will catch up next
	// time the orchestrator polls / the user re-views the plan.
	if err := writeOrchestratorSessionID(store, plan, sessionID); err != nil {
		// Don't roll back the launched session — log via the error
		// chain and let the caller surface it.
		return nil, fmt.Errorf("planstart: stamp session_id on plan %s: %w", planID, err)
	}
	if plan.Status == "todo" {
		if err := store.TransitionTask(planID, "doing"); err != nil {
			return nil, fmt.Errorf("planstart: transition plan %s to doing: %w", planID, err)
		}
	}

	return &Result{
		SessionID: sessionID,
		PlanID:    planID,
		StartedAt: time.Now().UTC(),
	}, nil
}

// startableStatus is the closed set of plan statuses that permit a
// fresh orchestrator launch. `doing` is excluded because that's the
// idempotency case (handled separately upstream); `done` / `blocked`
// / `abandoned` are terminal — re-running a finished plan is a V1.1
// concern.
func startableStatus(s string) bool {
	switch s {
	case "todo", "review":
		return true
	}
	return false
}

// sessionTerminal reports whether a session row's state is a sink in
// the sessionmgr lifecycle. Mirrors sessionmgr.Status.Terminal but
// avoids importing the package's exported Status type into a string-
// comparison hot path.
func sessionTerminal(state string) bool {
	switch state {
	case "done", "failed", "crashed":
		return true
	}
	return false
}

// readOrchestratorSessionID extracts metadata.plan.orchestrator_session_id
// from the task's metadata blob. Returns ("", false) when absent.
func readOrchestratorSessionID(plan *sqlstore.TaskRecord) (string, bool) {
	if !plan.Metadata.Valid || plan.Metadata.String == "" {
		return "", false
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(plan.Metadata.String), &root); err != nil {
		return "", false
	}
	planNS, _ := root["plan"].(map[string]any)
	v, _ := planNS["orchestrator_session_id"].(string)
	return v, v != ""
}

// writeOrchestratorSessionID merges the session id into the plan's
// metadata.plan namespace without clobbering pre-existing keys
// (phases, planner_refinement, etc.).
func writeOrchestratorSessionID(store Store, plan *sqlstore.TaskRecord, sessionID string) error {
	root := map[string]any{}
	if plan.Metadata.Valid && plan.Metadata.String != "" {
		_ = json.Unmarshal([]byte(plan.Metadata.String), &root)
	}
	planNS, _ := root["plan"].(map[string]any)
	if planNS == nil {
		planNS = map[string]any{}
	}
	planNS["orchestrator_session_id"] = sessionID
	planNS["orchestrator_started_at"] = time.Now().UTC().Format(time.RFC3339)
	root["plan"] = planNS
	body, err := json.Marshal(root)
	if err != nil {
		return err
	}
	metaNS := sql.NullString{String: string(body), Valid: true}
	return store.UpdateTask(plan.ID, sqlstore.TaskUpdate{Metadata: &metaNS})
}

func nullStr(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return s.String
}
