import type { ConfirmResult } from '../seams/identity-confirm'
import type { AgentTemplate, AssignmentRecord, LifecyclePlanRequest, RetireImpact, TeamRecord } from './lifecycle'
import type { AgentPlan, ApplyOutcome, AuditEntry, PlanRequest } from './plan'
import type { AgentListQuery, AgentRecord } from './types'

/**
 * Seam for the agent directory and manager. The mock implements it today;
 * a typed HTTP client replaces it once the backend exposes agent definitions
 * (see BACKEND_NEEDS.md).
 *
 * Writes follow plan -> approve -> apply. `approvePlan`, `applyPlan` and
 * `rollbackApply` each take the IdentityConfirm result the ActionBus obtained
 * for that exact step; the backend verifies `confirmation.token`, so a client
 * cannot approve or apply without a fresh grant. Plans never change an agent.
 */
export interface AgentsClient {
  listAgents(query?: AgentListQuery): Promise<AgentRecord[]>
  getAgent(id: string): Promise<AgentRecord | null>

  /** Compute a diff, risk flags and validation for a draft. Side-effect free apart from recording the plan. */
  planChange(req: PlanRequest): Promise<AgentPlan>
  getPlan(planId: string): Promise<AgentPlan | null>
  /** Marks the plan approved (time-boxed). Needs a granted step-up. */
  approvePlan(planId: string, confirmation: ConfirmResult): Promise<AgentPlan>
  /** Writes the definition. Needs an approved, unexpired plan and a fresh granted step-up. */
  applyPlan(planId: string, confirmation: ConfirmResult): Promise<ApplyOutcome>
  /** Restores the definition from before an applied plan. Needs a fresh granted step-up. */
  rollbackApply(planId: string, confirmation: ConfirmResult): Promise<ApplyOutcome>
  listAudit(agentId: string): Promise<AuditEntry[]>

  // ---- B3 lifecycle (additive) ------------------------------------------------
  // Create, clone, retire/restore, team CRUD and assignment changes are plans:
  // `planLifecycle` returns the same AgentPlan (with `lifecycle` detail) and the
  // plan is then approved and applied with approvePlan/applyPlan/rollbackApply
  // and the same per-step IdentityConfirm. Nothing here writes on its own.
  // For team plans `AgentPlan.agentId` is the team id; for assignment plans it is
  // the single agent touched or "assignments"; listAudit takes those ids too.
  planLifecycle(req: LifecyclePlanRequest): Promise<AgentPlan>
  /** Sessions, tasks, workstreams and teams a retire would affect. Read-only. */
  getRetireImpact(agentId: string): Promise<RetireImpact>
  listTemplates(): Promise<AgentTemplate[]>
  listTeams(): Promise<TeamRecord[]>
  getTeam(id: string): Promise<TeamRecord | null>
  listAssignments(query?: { agentId?: string; targetId?: string }): Promise<AssignmentRecord[]>
}
