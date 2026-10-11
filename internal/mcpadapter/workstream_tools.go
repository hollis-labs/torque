package mcpadapter

import (
	"context"

	"github.com/hollis-labs/torque/internal/service"
)

const workstreamClaimNextDescription = `Claim the next cleared workstream for a team run (a parent task with children). Torque owns the queue; the caller owns the team run.
Use when a team run is free and wants work. One atomic step applies go/pause (the scheduler's enabled flag), the per-project concurrent-claim cap and eligibility (status todo, manual=false, kind parent with children, dependencies done, no active claim), records the claim and moves the task to doing. The claim is a lease: renew it with torque_workstream_heartbeat while the run is alive.
Response shape: data = {outcome, claim_id?, task_id?, lease_expires_at?, reason?, diagnostics?}. outcome is dispatch (you hold the claim), defer (reason: paused, project_cap or no_cleared_work; ask again after a wake or later) or refuse (invalid request). team_run_id is the Tether run id, required; team_id is the definition id and is recorded only. A repeat call with the same team_run_id and idempotency_key returns the same claim. This tool needs the serve process scheduler; stdio MCP clients should call POST /api/v1/workstreams/claim.
Example: {"team_run_id":"3f2a...","team_id":"task-team-a","actor":"coordinator","idempotency_key":"claim-1"}`

const workstreamHeartbeatDescription = `Extend a workstream claim's lease while its team run is alive.
Use periodically from the team run's owner. Torque never reclaims a claim on silence alone; an expired lease is surfaced for review, not reassigned.
Response shape: data = {claim_id, lease_expires_at}. Fails if the claim is released.
Example: {"claim_id":"clm_1a2b3c4d5e6f7a8b"}`

const workstreamReleaseDescription = `End a workstream claim with an outcome.
Use when the team run finishes or gives up. completed asks for done and passes the landing gate (on_done_merge and ci_green evidence): missing evidence leaves the task blocked and escalated, and the release still succeeds. failed blocks the task. canceled and handoff return it to the queue.
Response shape: data = {claim_id, task_id, task_status, reason?}; reason carries a landing-gate refusal. Releasing twice is a conflict.
Example: {"claim_id":"clm_1a2b3c4d5e6f7a8b","outcome":"completed","result_refs":"[\"https://github.com/acme/app/pull/42\"]"}`

func (a *Adapter) registerWorkstreamTools() {
	a.addTool(newTool("torque_workstream_claim_next",
		withDescription(workstreamClaimNextDescription),
		withString("team_run_id", required(), desc("Tether run id of the free team run (not the team definition id).")),
		withString("team_id", desc("Team definition id; recorded on the claim.")),
		withString("actor", desc("Authenticated actor making the claim.")),
		withString("environment_id", desc("Environment the run is placed in; recorded on the claim.")),
		withArray("project_scope", desc("Project ids to take work from; omit for any project. (string array)")),
		withString("idempotency_key", desc("Makes a retried claim return the same lease.")),
	), a.handleWorkstreamClaimNext)

	a.addTool(newTool("torque_workstream_heartbeat",
		withDescription(workstreamHeartbeatDescription),
		withString("claim_id", required(), desc("Claim to renew.")),
	), a.handleWorkstreamHeartbeat)

	a.addTool(newTool("torque_workstream_release",
		withDescription(workstreamReleaseDescription),
		withString("claim_id", required(), desc("Claim to end.")),
		withString("outcome", required(), desc("completed, failed, canceled or handoff.")),
		withString("result_refs", desc("JSON array of result references (PR URLs, artifacts).")),
	), a.handleWorkstreamRelease)
}

func (a *Adapter) handleWorkstreamClaimNext(ctx context.Context, req map[string]any) (any, error) {
	if a.sched == nil {
		return errResult(ErrCodeDomain, "workstream claims need the serve process scheduler for go/pause; stdio mcp cannot see it, use POST /api/v1/workstreams/claim", "")
	}
	scope, err := reqStrSlice(req, "project_scope")
	if err != nil {
		return errResult(ErrCodeArgInvalid, err.Error(), "project_scope")
	}
	res, err := a.svc.Workstream.ClaimNext(ctx, service.ClaimInput{
		TeamID: reqStr(req, "team_id"), TeamRunID: reqStr(req, "team_run_id"),
		Actor: reqStr(req, "actor"), EnvironmentID: reqStr(req, "environment_id"),
		ProjectScope: scope, IdempotencyKey: reqStr(req, "idempotency_key"),
	})
	if err != nil {
		code, msg, field := mapServiceError(err)
		return errResult(code, msg, field)
	}
	return okResult(res)
}

func (a *Adapter) handleWorkstreamHeartbeat(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "claim_id")
	until, err := a.svc.Workstream.Heartbeat(ctx, id)
	if err != nil {
		code, msg, field := mapServiceError(err)
		return errResult(code, msg, field)
	}
	return okResult(map[string]any{"claim_id": id, "lease_expires_at": until})
}

func (a *Adapter) handleWorkstreamRelease(ctx context.Context, req map[string]any) (any, error) {
	res, err := a.svc.Workstream.Release(ctx, reqStr(req, "claim_id"), reqStr(req, "outcome"), reqStr(req, "result_refs"))
	if err != nil {
		code, msg, field := mapServiceError(err)
		return errResult(code, msg, field)
	}
	return okResult(res)
}
