// ActionBus: every user-triggered action (from an event's offered actions or a
// feature's own buttons) is dispatched here. Handlers register per action kind.
// Actions that authorise a change carry a `confirm` requirement; the bus runs
// IdentityConfirm first and never calls the handler without a valid grant, so
// no feature can forget the step-up.

import type { ConsoleRef } from '../events/types'
import type { ConfirmRequest, ConfirmResult, IdentityConfirm } from './identity-confirm'
import { isGranted } from './identity-confirm'

export interface ConsoleAction {
  /** Action kind, matched against registered handlers: "approve", "navigate", "dismiss"... */
  kind: string
  /** Offer id when the action came from an event. */
  actionId?: string
  eventId?: string
  subject?: ConsoleRef
  params?: Record<string, unknown>
  /** Present => IdentityConfirm runs before the handler. */
  confirm?: Pick<ConfirmRequest, 'what' | 'scope' | 'ttlMs' | 'who'>
}

export type ActionStatus = 'ok' | 'error' | 'denied' | 'unhandled'

export interface ActionResult {
  status: ActionStatus
  message?: string
  data?: unknown
  /** The step-up outcome, when one ran. */
  confirmation?: ConfirmResult
}

export interface ActionContext {
  /** Present when the action required (and received) step-up. Forward `confirmation.token` to the backend. */
  confirmation?: ConfirmResult
}

export type ActionHandler = (action: ConsoleAction, ctx: ActionContext) => Promise<Omit<ActionResult, 'confirmation'> | void> | Omit<ActionResult, 'confirmation'> | void

export interface ActionRecord {
  action: ConsoleAction
  result: ActionResult
  at: number
}

export interface ActionBus {
  dispatch(action: ConsoleAction): Promise<ActionResult>
  /** Last registration for a kind wins; returns unregister. */
  register(kind: string, handler: ActionHandler): () => void
  /** Observe every completed dispatch (audit trail, toasts, state-store settle). */
  subscribe(listener: (r: ActionRecord) => void): () => void
}

export function createActionBus(opts: { identity: IdentityConfirm; now?: () => number }): ActionBus {
  const handlers = new Map<string, ActionHandler>()
  const listeners = new Set<(r: ActionRecord) => void>()
  const now = opts.now ?? Date.now

  const finish = (action: ConsoleAction, result: ActionResult): ActionResult => {
    const rec = { action, result, at: now() }
    listeners.forEach((l) => l(rec))
    return result
  }

  return {
    register(kind, handler) {
      handlers.set(kind, handler)
      return () => {
        if (handlers.get(kind) === handler) handlers.delete(kind)
      }
    },
    subscribe(l) {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    async dispatch(action) {
      const handler = handlers.get(action.kind)
      if (!handler) return finish(action, { status: 'unhandled', message: `No handler for "${action.kind}"` })
      let confirmation: ConfirmResult | undefined
      if (action.confirm) {
        try {
          confirmation = await opts.identity.confirm(action.confirm)
        } catch (e) {
          return finish(action, { status: 'denied', message: e instanceof Error ? e.message : 'Identity confirmation failed' })
        }
        if (!isGranted(confirmation, now())) {
          return finish(action, { status: 'denied', message: confirmation.reason ?? `Confirmation ${confirmation.outcome}`, confirmation })
        }
      }
      try {
        const out = (await handler(action, { confirmation })) ?? { status: 'ok' as const }
        return finish(action, { ...out, confirmation })
      } catch (e) {
        return finish(action, { status: 'error', message: e instanceof Error ? e.message : 'Action failed', confirmation })
      }
    },
  }
}

/** Builds a bus action from an event's offered action, wiring the step-up requirement from the offer. */
export function actionFromOffer(
  offer: { id: string; label: string; kind: string; requiresConfirm?: boolean; scope?: string[]; params?: Record<string, unknown> },
  ctx: { eventId?: string; subject?: ConsoleRef },
): ConsoleAction {
  return {
    kind: offer.kind,
    actionId: offer.id,
    eventId: ctx.eventId,
    subject: ctx.subject,
    params: offer.params,
    ...(offer.requiresConfirm
      ? { confirm: { what: { action: `${offer.kind}:${offer.id}`, title: offer.label, subject: ctx.subject }, scope: offer.scope ?? [] } }
      : {}),
  }
}
