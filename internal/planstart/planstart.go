// Package planstart implements the V0 trigger that boots an Orchestrator
// session for a kind=plan task (CW-20260503-0017, S2.1). HTTP
// (/api/v1/plans/{id}/start) and MCP (torque_plan_start) both call
// Start; the GUI's Execute Plan button hits the HTTP route.
//
// V0 wires the trigger as a hardcoded MCP tool + HTTP route. Refactor
// onto Plans v2 lifecycle hooks (phase_start / plan_complete) is
// deferred to CW-20260417-0135.
//
// Post-CW-20260508-0001: Start is a thin wrapper around agent.Manager.Boot.
// The first-turn-fire that previously had to follow Launch (the gap
// CW-20260507-0011 patched with a manual SendInput) now lives natively
// inside Boot via go-agent-sessions v0.6.0's AutoFireFirstTurn — there is
// no separate kickoff step. The package shrunk from ~260 LOC to ~140 LOC.
package planstart

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hollis-labs/torque/internal/orchestrator"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// Sentinel errors for the trigger. Handlers map them to HTTP status:
//   - ErrPlanNotFound      → 422 (plan id is not a kind=plan task)
//   - ErrPlanWrongStatus   → 422 (plan in done/blocked/abandoned)
//   - ErrAlreadyOrchestrating → 409 (an orchestrator session is live)
//   - ErrWorkdirRequired   → 422 (Options.Workdir empty AND plan.WorkingDir empty)
//   - ErrSessionMgrMissing → 503 (substrate not wired into this host)
//
// ErrFirstTurnFailed (the prior 502 mapping) is gone — agent.Boot owns the
// first-turn drive natively via AutoFireFirstTurn, so a partial-launch
// failure is just an ErrBootFailed wrapping the underlying cause; handlers
// map it to 500 like any other Boot failure.
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

// Result is what Start returns on success. Mirrors the documented HTTP/MCP
// response: session_id + plan_id + an RFC3339 started_at stamp the GUI/CLI
// shows in the "Execute Plan" toast.
type Result struct {
	SessionID string    `json:"session_id"`
	PlanID    string    `json:"plan_id"`
	StartedAt time.Time `json:"started_at"`
}

// Store is the narrowed dependency surface Start uses. Defined as an
// interface so tests can substitute an in-memory fake without dragging the
// full sqlstore.Store into the test harness. Production passes
// *sqlstore.Store directly — it satisfies this contract.
type Store interface {
	GetTask(id string) (*sqlstore.TaskRecord, error)
	UpdateTask(id string, u sqlstore.TaskUpdate) error
	TransitionTask(id, newStatus string) error
	GetSession(id string) (*sqlstore.SessionRecord, error)
	// AppendRunEvent records the recovery.pack_planted breadcrumb on the
	// Redispatch cold-boot path. Errors are non-fatal (logged, never block
	// the redispatch) — observability must not gate recovery.
	AppendRunEvent(evt *sqlstore.RunEventRecord) (int64, error)
}

// SessionManager is the narrowed agent.Manager surface. The real
// *agent.Manager implements it; tests pass a stub. Boot drives the
// orchestrator session — no separate SendInput kickoff step.
type SessionManager interface {
	Boot(ctx context.Context, opts agent.Options) (*agent.Session, error)
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
		// with a fresh boot. Avoids leaving the plan stuck on a
		// crashed/orphaned session id forever.
	}

	workdir := opts.Workdir
	if workdir == "" {
		workdir = plan.WorkingDir
	}
	if workdir == "" {
		return nil, ErrWorkdirRequired
	}
	// Persist the resolved workdir back to the plan task when it diverges
	// from the stored value. Future child sub-task creations (planner /
	// reviewer / orchestrator-spawned kind=internal tasks) inherit
	// working_dir from parent_id (CW-20260508-0004); the plan task IS the
	// parent for the planner sub-task, so its WorkingDir column must be
	// the resolved value or inheritance falls back to empty and the
	// scheduler rejects child dispatch with "working_dir is required".
	if plan.WorkingDir != workdir {
		if err := store.UpdateTask(planID, sqlstore.TaskUpdate{WorkingDir: &workdir}); err != nil {
			return nil, fmt.Errorf("planstart: persist workdir on plan %s: %w", planID, err)
		}
	}

	bootOpts := agent.Options{
		Mode:         agent.ModeLongLived,
		AgentProfile: orchestrator.Profile,
		Role:         orchestrator.SessionMetaRoleValue,
		Workdir:      workdir,
		ProjectID:    nullStr(plan.ProjectID),
		TaskID:       planID,
		SystemPrompt: orchestrator.SystemPromptForPlan(planID, ""),
		Env:          envSliceToMap(opts.Env),
		SessionMeta: map[string]string{
			orchestrator.SessionMetaRole:   orchestrator.SessionMetaRoleValue,
			orchestrator.SessionMetaPlanID: planID,
		},
	}

	// Boot drives Manager.Start with AutoFireFirstTurn=true for ModeLongLived,
	// so the orchestrator's first turn (the kickoff "Boot @./boot.md") is
	// in flight by the time Boot returns. No separate SendInput call needed.
	sess, err := mgr.Boot(ctx, bootOpts)
	if err != nil {
		// agent.Boot wraps with ErrBootFailed for any setup failure
		// (loopback, boot dir, runtime, Start). Surface verbatim — the
		// session row was rolled back inside Boot, so nothing to clean up
		// here. The plan stays in todo and the caller can retry.
		return nil, fmt.Errorf("planstart: boot orchestrator: %w", err)
	}

	// Stamp orchestrator_session_id into metadata.plan and transition
	// plan → doing. Both writes are best-effort cleanup if the second
	// fails: the session is already running and visible via
	// torque_session_list; the plan's status will catch up next time
	// the orchestrator polls / the user re-views the plan.
	if err := writeOrchestratorSessionID(store, plan, sess.ID); err != nil {
		// Don't roll back the launched session — log via the error
		// chain and let the caller surface it.
		return nil, fmt.Errorf("planstart: stamp session_id on plan %s: %w", planID, err)
	}
	if plan.Status == "todo" {
		if err := store.TransitionTask(planID, "doing"); err != nil {
			return nil, fmt.Errorf("planstart: transition plan %s to doing: %w", planID, err)
		}
	}

	// Use the persisted session row's CreatedAt so the trigger's response
	// matches the idempotency path (which surfaces rec.CreatedAt) and avoids
	// a clock-skew between time.Now() here and the session row's stamp. Per
	// Copilot review feedback on PR #19.
	startedAt := sess.CreatedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	return &Result{
		SessionID: sess.ID,
		PlanID:    planID,
		StartedAt: startedAt,
	}, nil
}

// Redispatch re-boots the Orchestrator session for a plan whose previous
// orchestrator session has exited/paused — the recovery path after a HITL
// checkpoint on a child task is responded (CW-20260518 orchestrator
// checkpoint-redispatch fix).
//
// Difference from Start: Start is the operator-driven first launch and is
// gated to plans in `todo`/`review`. Redispatch is the substrate-driven
// continuation of a plan the orchestrator was already walking — the plan is
// typically still `doing` (the orchestrator emitted a session-complete
// marker and exited mid-walk while waiting on a checkpoint). Redispatch is
// therefore NOT gated on plan status; it only refuses terminal plans
// (done/blocked/abandoned), where re-running is meaningless.
//
// Idempotency: if metadata.plan.orchestrator_session_id names a session
// that is still live, Redispatch returns ErrAlreadyOrchestrating wrapping a
// Result (same shape as Start) — the live orchestrator will pick up the
// checkpoint response via its own redispatch-preflight poll, so a second
// boot would be a duplicate walker.
//
// On a fresh boot Redispatch re-stamps metadata.plan.orchestrator_session_id
// and leaves the plan's status untouched (a plan mid-walk is already in the
// right status; Start's todo→doing transition does not apply here).
func Redispatch(ctx context.Context, store Store, mgr SessionManager, planID string, opts Options) (*Result, error) {
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
	if terminalStatus(plan.Status) {
		return nil, fmt.Errorf("%w: status=%q", ErrPlanWrongStatus, plan.Status)
	}

	// Idempotency: a still-live orchestrator session needs no redispatch —
	// it will see the checkpoint response on its next preflight poll.
	//
	// priorSession captures the EXITED orchestrator session row (when present
	// and terminal) so the cold-boot recovery pack below can replay its
	// trailing turns into the freshly-booted orchestrator. This is torque's
	// cold-boot-with-prior-history path: Redispatch re-boots a session whose
	// process is gone, on a plan it was mid-walk.
	var priorSession *sqlstore.SessionRecord
	if existing, ok := readOrchestratorSessionID(plan); ok && existing != "" {
		if rec, err := store.GetSession(existing); err == nil {
			if !sessionTerminal(rec.State) {
				return &Result{
					SessionID: existing,
					PlanID:    planID,
					StartedAt: rec.CreatedAt,
				}, fmt.Errorf("%w: session=%s", ErrAlreadyOrchestrating, existing)
			}
			// Terminal prior session — eligible for recovery replay.
			priorSession = rec
		}
		// Stale/terminal/missing session row — proceed with a fresh boot.
	}

	workdir := opts.Workdir
	if workdir == "" {
		workdir = plan.WorkingDir
	}
	if workdir == "" {
		return nil, ErrWorkdirRequired
	}
	if plan.WorkingDir != workdir {
		if err := store.UpdateTask(planID, sqlstore.TaskUpdate{WorkingDir: &workdir}); err != nil {
			return nil, fmt.Errorf("planstart: persist workdir on plan %s: %w", planID, err)
		}
	}

	systemPrompt := orchestrator.SystemPromptForPlan(planID, "")

	// Cold-boot recovery pack. When the prior orchestrator session is terminal
	// and left a turn trace, build a bounded recovered-context block and
	// prepend it to the orchestrator's system prompt so the fresh boot resumes
	// from recovered context instead of starting blind. The pack lands in the
	// planted boot.md (composeSystemPrompt → kickoffMarkdown surfaces
	// Options.SystemPrompt under "Task framing"), the agent reads on first
	// turn, and the full pack is also written to <bootDir>/recovery.md
	// post-Boot below. Best-effort — a read error never blocks the redispatch.
	recoveryPack, recoveryTurns, recErr := agent.BuildRecoveryPackForPriorSession(
		priorSession,
		"orchestrator redispatch after prior session exited (cold boot with prior history)",
	)
	if recErr != nil {
		log.Printf("planstart: recovery pack read for plan=%s prior_session=%s: %v",
			planID, priorSessionID(priorSession), recErr)
	}
	if recoveryPack != "" {
		systemPrompt = recoveryPack + "\n\n" + systemPrompt
	}

	bootOpts := agent.Options{
		Mode:         agent.ModeLongLived,
		AgentProfile: orchestrator.Profile,
		Role:         orchestrator.SessionMetaRoleValue,
		Workdir:      workdir,
		ProjectID:    nullStr(plan.ProjectID),
		TaskID:       planID,
		SystemPrompt: systemPrompt,
		Env:          envSliceToMap(opts.Env),
		SessionMeta: map[string]string{
			orchestrator.SessionMetaRole:   orchestrator.SessionMetaRoleValue,
			orchestrator.SessionMetaPlanID: planID,
		},
	}

	sess, err := mgr.Boot(ctx, bootOpts)
	if err != nil {
		return nil, fmt.Errorf("planstart: redispatch orchestrator: %w", err)
	}

	// Plant the full pack to <bootDir>/recovery.md as a re-readable pointer and
	// emit the recovery.pack_planted breadcrumb. Both best-effort: a failed
	// write/emit must not roll back an already-running orchestrator.
	if recoveryPack != "" {
		if packPath, werr := agent.WriteRecoveryPackFile(sess.BootDir, recoveryPack); werr != nil {
			log.Printf("planstart: write recovery.md for plan=%s session=%s path=%s: %v",
				planID, sess.ID, packPath, werr)
		}
		emitRecoveryPackPlanted(store, planID, sess.ID, priorSessionID(priorSession), recoveryTurns)
	}

	if err := writeOrchestratorSessionID(store, plan, sess.ID); err != nil {
		return nil, fmt.Errorf("planstart: stamp session_id on plan %s: %w", planID, err)
	}

	startedAt := sess.CreatedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	return &Result{
		SessionID: sess.ID,
		PlanID:    planID,
		StartedAt: startedAt,
	}, nil
}

// priorSessionID returns the prior session id for logging, "" when nil.
func priorSessionID(rec *sqlstore.SessionRecord) string {
	if rec == nil {
		return ""
	}
	return rec.ID
}

// recoveryPackPlantedPayload is the JSON shape stored on the run_events
// breadcrumb for a planted recovery pack. Postmortem queries grep by
// type="recovery.pack_planted".
type recoveryPackPlantedPayload struct {
	PlanID         string `json:"plan_id"`
	NewSessionID   string `json:"new_session_id"`
	PriorSessionID string `json:"prior_session_id,omitempty"`
	Turns          int    `json:"turns"`
}

// emitRecoveryPackPlanted appends a recovery.pack_planted run_events row so the
// cold-boot recovery is queryable. Mirrors nanite's recovery_pack_planted
// LogEvent. Failure to append is logged but never propagated — observability
// must not block the redispatch.
func emitRecoveryPackPlanted(store Store, planID, newSessionID, priorSessionID string, turns int) {
	body, err := json.Marshal(recoveryPackPlantedPayload{
		PlanID:         planID,
		NewSessionID:   newSessionID,
		PriorSessionID: priorSessionID,
		Turns:          turns,
	})
	if err != nil {
		log.Printf("planstart: marshal recovery.pack_planted for plan=%s: %v", planID, err)
		return
	}
	if _, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
		TaskID:  planID,
		Type:    "recovery.pack_planted",
		Payload: string(body),
	}); err != nil {
		log.Printf("planstart: append recovery.pack_planted for plan=%s: %v", planID, err)
	}
}

// terminalStatus reports whether a plan status is a lifecycle sink where a
// redispatch is meaningless.
func terminalStatus(s string) bool {
	switch s {
	case "done", "blocked", "abandoned", "cancelled":
		return true
	}
	return false
}

// startableStatus is the closed set of plan statuses that permit a fresh
// orchestrator boot. `doing` is excluded because that's the idempotency
// case (handled separately upstream); `done` / `blocked` / `abandoned` are
// terminal — re-running a finished plan is a V1.1 concern.
func startableStatus(s string) bool {
	switch s {
	case "todo", "review":
		return true
	}
	return false
}

// sessionTerminal reports whether a session row's state is a sink in the
// agent lifecycle. Mirrors agent.Status.Terminal but avoids importing the
// package's exported Status type into a string-comparison hot path.
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
// metadata.plan namespace without clobbering pre-existing keys (phases,
// planner_refinement, etc.).
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

// envSliceToMap converts the legacy []string "K=V" env shape (carried by
// the HTTP/MCP API for back-compat) into the map[string]string shape
// agent.Options expects. Malformed entries (no '=') are silently skipped.
func envSliceToMap(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				out[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return out
}
