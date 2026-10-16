// ActionBus handlers for HITL actions. Approve goes through the bus with a
// `confirm` requirement (IdentityConfirm runs first, the bus never calls the
// handler without a grant) and forwards the grant to the client.
import type { ActionBus, ActionHandler } from '../seams/action-bus'
import { secretHandoff, type HitlClient, type SecretHandoff } from './client'
import { HITL_ACTION } from './model'
import type { HitlFormValues } from './types'

const reqId = (p: Record<string, unknown> | undefined): string => {
  if (typeof p?.requestId !== 'string') throw new Error('Missing request id')
  return p.requestId
}

export function registerHitlHandlers(bus: ActionBus, client: HitlClient, handoff: SecretHandoff = secretHandoff): () => void {
  const reason = (p: Record<string, unknown> | undefined) => (typeof p?.reason === 'string' && p.reason.trim() ? p.reason.trim() : undefined)
  const decide = (decision: 'approve' | 'reject' | 'defer', msg: string): ActionHandler => async (a, ctx) => {
    await client.respond(reqId(a.params), { type: 'approval', decision, reason: reason(a.params) }, { confirmation: ctx.confirmation })
    return { status: 'ok', message: msg }
  }
  const offs = [
    bus.register(HITL_ACTION.approve, decide('approve', 'Approved')),
    bus.register(HITL_ACTION.reject, decide('reject', 'Rejected')),
    bus.register(HITL_ACTION.defer, decide('defer', 'Deferred')),
    bus.register(HITL_ACTION.answer, async (a, ctx) => {
      const answer = a.params?.answer
      if (typeof answer !== 'string' && !(Array.isArray(answer) && answer.every((x) => typeof x === 'string'))) throw new Error('Missing answer')
      await client.respond(reqId(a.params), { type: 'question', answer: answer as string | string[] }, { confirmation: ctx.confirmation })
      return { status: 'ok', message: 'Answer sent' }
    }),
    bus.register(HITL_ACTION.form, async (a, ctx) => {
      const values = a.params?.values
      if (typeof values !== 'object' || values === null) throw new Error('Missing form values')
      await client.respond(reqId(a.params), { type: 'form', values: values as HitlFormValues }, { confirmation: ctx.confirmation })
      return { status: 'ok', message: 'Form submitted' }
    }),
    // Secret: params carry only an opaque handle. The value is taken once, passed straight to the client and never returned or thrown.
    bus.register(HITL_ACTION.secret, async (a, ctx) => {
      const handle = a.params?.handle
      const value = typeof handle === 'string' ? handoff.take(handle) : undefined
      if (!value) throw new Error('Secret was not available; enter it again')
      await client.submitSecret(reqId(a.params), value, { confirmation: ctx.confirmation })
      return { status: 'ok', message: 'Secret delivered' }
    }),
  ]
  return () => offs.forEach((o) => o())
}
