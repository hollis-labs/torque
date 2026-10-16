// ActionBus wiring for the agent editor. Approve, apply and rollback each
// carry a `confirm` requirement, so the bus runs IdentityConfirm first and the
// handler below only ever sees a granted step-up. The confirmation is
// forwarded to the client, which hands the token to the backend.

import type { ActionBus, ConsoleAction } from '../seams/action-bus'
import type { AgentsClient } from './client'
import { OP_LABEL } from './lifecycle'
import type { AgentPlan } from './plan'

export const AGENT_ACTIONS = {
  approve: 'agent.plan.approve',
  apply: 'agent.plan.apply',
  rollback: 'agent.plan.rollback',
} as const
export type AgentActionKind = (typeof AGENT_ACTIONS)[keyof typeof AGENT_ACTIONS]

const TITLES: Record<AgentActionKind, string> = {
  'agent.plan.approve': 'Approve changes to',
  'agent.plan.apply': 'Apply changes to',
  'agent.plan.rollback': 'Roll back changes to',
}

/** Step-up grants for agent changes stay short; apply must follow soon after. */
const CONFIRM_TTL_MS = 2 * 60_000

export function agentPlanAction(kind: AgentActionKind, plan: AgentPlan, agentName: string): ConsoleAction {
  const n = plan.changes.length
  const lc = plan.lifecycle
  const op = lc?.request.op
  const subject = { type: op?.startsWith('team.') ? 'team' : op === 'assignment.update' ? 'assignment' : 'agent', id: plan.agentId, label: agentName }
  const flagged = `${plan.risk} risk${plan.risks.some((r) => r.level === 'high') ? ' · privileged' : ''}`
  return {
    kind,
    subject,
    params: { planId: plan.id },
    confirm: {
      what: {
        action: kind,
        // Lifecycle plans name the operation so the step-up prompt says what is being authorised.
        title: lc && op ? `${TITLES[kind].replace(/ (changes )?to$/, '')}: ${OP_LABEL[op]} ${agentName}`.trim() : `${TITLES[kind]} ${agentName}`,
        summary: lc ? `${lc.summary} · ${flagged}` : `${n} change${n === 1 ? '' : 's'} · ${flagged}`,
        subject,
      },
      scope: plan.requiredScope,
      ttlMs: CONFIRM_TTL_MS,
    },
  }
}

/** Registers the three handlers; returns an unregister. */
export function registerAgentActions(bus: ActionBus, client: AgentsClient): () => void {
  const planId = (a: ConsoleAction) => String(a.params?.planId ?? '')
  const need = (c: unknown) => {
    if (!c) throw new Error('Missing step-up confirmation')
    return c as import('../seams/identity-confirm').ConfirmResult
  }
  const offs = [
    bus.register(AGENT_ACTIONS.approve, async (a, ctx) => {
      const plan = await client.approvePlan(planId(a), need(ctx.confirmation))
      return { status: 'ok', data: plan }
    }),
    bus.register(AGENT_ACTIONS.apply, async (a, ctx) => {
      const out = await client.applyPlan(planId(a), need(ctx.confirmation))
      return out.status === 'applied' ? { status: 'ok', data: out } : { status: 'error', message: out.message, data: out }
    }),
    bus.register(AGENT_ACTIONS.rollback, async (a, ctx) => {
      const out = await client.rollbackApply(planId(a), need(ctx.confirmation))
      return out.status === 'rolled_back' ? { status: 'ok', data: out } : { status: 'error', message: out.message, data: out }
    }),
  ]
  return () => offs.forEach((o) => o())
}
