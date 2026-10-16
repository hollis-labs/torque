// ActionBus handlers for workflow actions. Step-up (IdentityConfirm) is declared
// on the card's action spec; the bus runs it before the handler and the handler
// forwards the grant to the client.
import type { ActionBus } from '../seams/action-bus'
import type { WorkflowClient } from './client'
import { WORKFLOW_ACTION } from './model'
import type { GateDecision, PlanDecision, SteerCommand } from './types'
import { STEER_TYPES } from './types'

const need = (p: Record<string, unknown> | undefined, k: string): string => {
  const v = p?.[k]
  if (typeof v !== 'string' || !v) throw new Error(`Missing ${k}`)
  return v
}

export function registerWorkflowHandlers(bus: ActionBus, client: WorkflowClient): () => void {
  const offs = [
    bus.register(WORKFLOW_ACTION.steer, async (a, ctx) => {
      const cmd = a.params?.command as SteerCommand | undefined
      if (!cmd || !STEER_TYPES.includes(cmd.type)) throw new Error('Missing steer command')
      await client.steer(need(a.params, 'runId'), cmd, { confirmation: ctx.confirmation })
      return { status: 'ok', message: { pause: 'Paused', resume: 'Resumed', redirect: 'Redirected', escalate: 'Escalated', priority: 'Priority changed' }[cmd.type] }
    }),
    bus.register(WORKFLOW_ACTION.gate, async (a, ctx) => {
      const d = need(a.params, 'decision') as GateDecision
      const note = typeof a.params?.note === 'string' ? a.params.note : undefined
      await client.submitGate(need(a.params, 'runId'), need(a.params, 'gateId'), d, note, { confirmation: ctx.confirmation })
      return { status: 'ok', message: { pass: 'Gate passed', fail: 'Gate failed', override: 'Gate overridden' }[d] }
    }),
    bus.register(WORKFLOW_ACTION.checkpoint, async (a, ctx) => {
      await client.respondCheckpoint(need(a.params, 'runId'), need(a.params, 'checkpointId'), need(a.params, 'answer'), { confirmation: ctx.confirmation })
      return { status: 'ok', message: 'Answer sent' }
    }),
    bus.register(WORKFLOW_ACTION.plan, async (a, ctx) => {
      const d = need(a.params, 'decision') as PlanDecision
      const included = Array.isArray(a.params?.included) ? (a.params.included as unknown[]).filter((x): x is string => typeof x === 'string') : []
      const note = typeof a.params?.note === 'string' ? a.params.note : undefined
      await client.decidePlan(need(a.params, 'runId'), d, included, note, { confirmation: ctx.confirmation })
      return { status: 'ok', message: d === 'approve' ? 'Plan approved' : 'Plan rejected' }
    }),
  ]
  return () => offs.forEach((o) => o())
}
