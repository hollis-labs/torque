package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/planstart"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/service"
)

// OrchestratorRedispatcher implements service.OrchestratorRedispatcher: when
// a HITL checkpoint is responded, it walks from the checkpoint's task up to
// the owning kind=plan ancestor and — if that plan was being walked by an
// Orchestrator session that has since exited/paused — re-boots the
// Orchestrator so the plan walk continues (CW-20260518 fix).
//
// The bug this fixes: a `pr_review` checkpoint emitted by a reviewer
// end-agent on a CHILD task is responded by a human. The α.4
// CheckpointResponseDispatcher only ever resumes the *checkpoint task's own*
// session — here that is the child's long-dead worker. Nothing wakes the
// Orchestrator, which is a SEPARATE session on the parent PLAN that emitted
// a `[system/orchestrator/session-complete]` marker and exited "expecting to
// be redispatched on merge". The plan stalls forever.
//
// Two things must happen for the Orchestrator to make progress:
//
//  1. The response must be reachable where the Orchestrator's
//     redispatch-preflight looks: `plan.metadata.checkpoint_responses`.
//     CheckpointService.attachCheckpointResponseToMetadata writes the
//     response onto the *checkpoint task* (the child). This redispatcher
//     copies it up onto the PLAN task's metadata so the preflight sees it.
//  2. The exited Orchestrator session must be re-booted. planstart.Redispatch
//     does the boot (idempotent against a still-live orchestrator).
//
// No-op cases (all cheap, all expected): the checkpoint task has no
// kind=plan ancestor; the plan never had an orchestrator session; the
// orchestrator session is still live; the plan is terminal.
type OrchestratorRedispatcher struct {
	store    *sqlstore.Store
	sessions *agent.Manager
}

// NewOrchestratorRedispatcher constructs a redispatcher bound to the
// daemon's store + agent.Manager. Both must be non-nil for the redispatcher
// to do anything; bootstrap.Reactor skips wiring when sessions is nil.
func NewOrchestratorRedispatcher(store *sqlstore.Store, sessions *agent.Manager) *OrchestratorRedispatcher {
	return &OrchestratorRedispatcher{store: store, sessions: sessions}
}

// planAncestorMaxDepth bounds the parent_id walk so a corrupt parent_id
// cycle can't spin forever. Plan→child is one hop in the V0 orchestrator;
// the planner/reviewer end-agents add one more under a child. 16 is far
// past any real lineage depth.
const planAncestorMaxDepth = 16

// RedispatchForCheckpointResponse implements service.OrchestratorRedispatcher.
func (d *OrchestratorRedispatcher) RedispatchForCheckpointResponse(ctx context.Context, in service.OrchestratorRedispatch) error {
	if d == nil || d.store == nil || d.sessions == nil {
		return nil
	}

	plan, err := d.planAncestor(in.TaskID)
	if err != nil {
		return fmt.Errorf("orchestrator redispatch: find plan ancestor of %s: %w", in.TaskID, err)
	}
	if plan == nil {
		// Not part of an orchestrated plan — nothing to redispatch.
		return nil
	}

	sessionID, ok := orchestratorSessionID(plan)
	if !ok || sessionID == "" {
		// The plan exists but was never walked by an orchestrator session
		// (e.g. a manually-driven plan). Nothing to redispatch.
		return nil
	}

	// Mirror the checkpoint response onto the PLAN task's metadata so the
	// orchestrator's redispatch-preflight (which reads
	// plan.metadata.checkpoint_responses) sees it. This is done before the
	// boot so a freshly-booted orchestrator's first preflight already has
	// the response in hand.
	if err := d.propagateResponseToPlan(plan, in.CorrelationID, in.ResponseJSON); err != nil {
		return fmt.Errorf("orchestrator redispatch: propagate response onto plan %s: %w", plan.ID, err)
	}

	// Re-boot the orchestrator. planstart.Redispatch is idempotent: a
	// still-live orchestrator session yields ErrAlreadyOrchestrating, which
	// is the success case here (the live orchestrator will pick the
	// response up on its own poll).
	res, err := planstart.Redispatch(ctx, d.store, d.sessions, plan.ID, planstart.Options{})
	if err != nil {
		if errors.Is(err, planstart.ErrAlreadyOrchestrating) {
			d.recordRedispatchEvent(plan.ID, in, sessionID, "", true, nil)
			return nil
		}
		d.recordRedispatchEvent(plan.ID, in, sessionID, "", false, err)
		return fmt.Errorf("orchestrator redispatch: re-boot orchestrator for plan %s: %w", plan.ID, err)
	}
	newSessionID := ""
	if res != nil {
		newSessionID = res.SessionID
	}
	d.recordRedispatchEvent(plan.ID, in, sessionID, newSessionID, false, nil)
	return nil
}

// orchestratorSessionID extracts metadata.plan.orchestrator_session_id from
// a plan task's metadata blob. Mirrors the unexported helpers in planstart
// and the agent session-lifecycle hook; duplicated here to keep the
// bootstrap package free of an import on either (planstart is imported for
// Redispatch only, and its reader is unexported).
func orchestratorSessionID(plan *sqlstore.TaskRecord) (string, bool) {
	if plan == nil || !plan.Metadata.Valid || plan.Metadata.String == "" {
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

// planAncestor walks parent_id from taskID upward and returns the first
// kind=plan task found. Returns (nil, nil) when the task has no kind=plan
// ancestor (and the task itself, if it IS a plan, is returned directly).
func (d *OrchestratorRedispatcher) planAncestor(taskID string) (*sqlstore.TaskRecord, error) {
	cur := taskID
	for depth := 0; depth < planAncestorMaxDepth && cur != ""; depth++ {
		task, err := d.store.GetTask(cur)
		if err != nil {
			if errors.Is(err, sqlstore.ErrTaskNotFound) {
				return nil, nil
			}
			return nil, err
		}
		if task.Kind == "plan" {
			return task, nil
		}
		if !task.ParentID.Valid || task.ParentID.String == "" {
			return nil, nil
		}
		cur = task.ParentID.String
	}
	return nil, nil
}

// propagateResponseToPlan merges the checkpoint response into the plan
// task's metadata.checkpoint_responses[correlationID]. Mirrors
// CheckpointService.attachCheckpointResponseToMetadata's shape so the
// orchestrator preflight reads one consistent structure regardless of
// whether the checkpoint was emitted on the plan itself or a child.
func (d *OrchestratorRedispatcher) propagateResponseToPlan(plan *sqlstore.TaskRecord, correlationID, responseJSON string) error {
	md := map[string]any{}
	if plan.Metadata.Valid && plan.Metadata.String != "" {
		_ = json.Unmarshal([]byte(plan.Metadata.String), &md)
	}
	responses, _ := md["checkpoint_responses"].(map[string]any)
	if responses == nil {
		responses = map[string]any{}
	}
	var parsed any
	if err := json.Unmarshal([]byte(responseJSON), &parsed); err != nil {
		parsed = responseJSON
	}
	responses[correlationID] = parsed
	md["checkpoint_responses"] = responses

	body, err := json.Marshal(md)
	if err != nil {
		return err
	}
	metaNS := sql.NullString{String: string(body), Valid: true}
	return d.store.UpdateTask(plan.ID, sqlstore.TaskUpdate{Metadata: &metaNS})
}

// redispatchEventPayload is the JSON shape stored on the run_events
// breadcrumb for an orchestrator redispatch. Postmortem queries grep by
// type="checkpoint.orchestrator_redispatched".
type redispatchEventPayload struct {
	CorrelationID    string `json:"correlation_id"`
	CheckpointTaskID string `json:"checkpoint_task_id"`
	PlanID           string `json:"plan_id"`
	PriorSessionID   string `json:"prior_session_id"`
	NewSessionID     string `json:"new_session_id,omitempty"`
	AlreadyLive      bool   `json:"already_live"`
	Error            string `json:"error,omitempty"`
}

// recordRedispatchEvent appends a single run_events row so the
// respond → propagate → redispatch chain is queryable. Failure to append is
// logged but never propagated — observability must not block the redispatch.
func (d *OrchestratorRedispatcher) recordRedispatchEvent(
	planID string, in service.OrchestratorRedispatch,
	priorSessionID, newSessionID string, alreadyLive bool, dispatchErr error,
) {
	if d == nil || d.store == nil {
		return
	}
	payload := redispatchEventPayload{
		CorrelationID:    in.CorrelationID,
		CheckpointTaskID: in.TaskID,
		PlanID:           planID,
		PriorSessionID:   priorSessionID,
		NewSessionID:     newSessionID,
		AlreadyLive:      alreadyLive,
	}
	if dispatchErr != nil {
		payload.Error = dispatchErr.Error()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[orchestrator-redispatch] marshal breadcrumb for plan=%s: %v", planID, err)
		return
	}
	evt := &sqlstore.RunEventRecord{
		TaskID:  planID,
		Type:    "checkpoint.orchestrator_redispatched",
		Payload: string(body),
	}
	if _, err := d.store.AppendRunEvent(evt); err != nil {
		log.Printf("[orchestrator-redispatch] append breadcrumb for plan=%s: %v", planID, err)
	}
}
