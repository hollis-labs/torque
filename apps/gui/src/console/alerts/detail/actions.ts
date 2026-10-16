// Alert action set (A2). Which actions an alert offers is declared per kind by a
// resolver (swappable like the event -> alert mapper), the pure helpers for
// snooze / defer times live here, and the approve / deny decision seam is a
// typed client with a mock until the backend exists (see BACKEND_NEEDS.md).

import { KINDS, type ConsoleEvent } from '../../events/types'
import type { ActionBus, ConsoleAction } from '../../seams/action-bus'
import type { Alert } from '../types'

export type AlertActionId = 'approve' | 'deny' | 'defer' | 'snooze' | 'settle' | 'reply' | 'dismiss'

export type AlertActionIntent = 'primary' | 'neutral' | 'danger'

export interface AlertActionSpec {
  id: AlertActionId
  label: string
  intent: AlertActionIntent
  /** Single key, handled while focus is inside the modal and not in a field. */
  shortcut: string
  /** Present => the ActionBus runs IdentityConfirm before the decision is sent. */
  confirm?: NonNullable<ConsoleAction['confirm']>
}

export type AlertActionResolver = (alert: Alert, event?: ConsoleEvent) => AlertActionSpec[]

const A: Record<AlertActionId, Omit<AlertActionSpec, 'confirm'>> = {
  approve: { id: 'approve', label: 'Approve', intent: 'primary', shortcut: 'a' },
  deny: { id: 'deny', label: 'Deny', intent: 'danger', shortcut: 'x' },
  reply: { id: 'reply', label: 'Reply', intent: 'neutral', shortcut: 'r' },
  defer: { id: 'defer', label: 'Defer', intent: 'neutral', shortcut: 'f' },
  snooze: { id: 'snooze', label: 'Snooze', intent: 'neutral', shortcut: 's' },
  settle: { id: 'settle', label: 'Settle', intent: 'neutral', shortcut: 'e' },
  dismiss: { id: 'dismiss', label: 'Dismiss', intent: 'neutral', shortcut: 'd' },
}

const spec = (id: AlertActionId, extra: Partial<AlertActionSpec> = {}): AlertActionSpec => ({ ...A[id], ...extra })

/**
 * Default per-kind action declaration. An event's own offered approve / reject
 * actions refine scope and confirmation (`requiresConfirm: false` opts out of
 * the step-up; the default for approve is to require it).
 */
export const defaultAlertActions: AlertActionResolver = (alert, event) => {
  const offer = (kind: string) => event?.actions.find((a) => a.kind === kind)
  const subject = event?.subjects.find((s) => s.type !== 'agent') ?? event?.subjects[0]
  switch (alert.kind) {
    case KINDS.approvalRequested: {
      const ap = offer('approve')
      const needsConfirm = ap?.requiresConfirm !== false
      return [
        spec('approve', needsConfirm ? { confirm: { what: { action: 'alert.approve', title: `Approve: ${alert.title}`, summary: alert.detail, subject }, scope: ap?.scope ?? ['approval.approve'] } } : {}),
        spec('deny', offer('reject')?.requiresConfirm ? { confirm: { what: { action: 'alert.deny', title: `Deny: ${alert.title}`, subject }, scope: offer('reject')?.scope ?? ['approval.deny'] } } : {}),
        spec('defer'),
        spec('snooze'),
        spec('reply'),
        spec('dismiss'),
      ]
    }
    case KINDS.messageReceived:
      return [spec('reply', { intent: 'primary' }), spec('defer'), spec('snooze'), spec('settle'), spec('dismiss')]
    default:
      return [spec('snooze'), spec('defer'), spec('settle'), spec('dismiss')]
  }
}

// --- time helpers (pure, `now` injected) ---------------------------------

export const MINUTE = 60_000
export const HOUR = 60 * MINUTE

export interface TimePreset {
  id: string
  label: string
  /** Absolute epoch ms. */
  until: number
}

/** Local wall-clock `hour:00` on the day `daysAhead` from `now`. */
export function localAt(now: number, hour: number, daysAhead = 0): number {
  const d = new Date(now)
  d.setDate(d.getDate() + daysAhead)
  d.setHours(hour, 0, 0, 0)
  return d.getTime()
}

export function snoozePresets(now: number): TimePreset[] {
  return [
    { id: '15m', label: '15 min', until: now + 15 * MINUTE },
    { id: '1h', label: '1 hour', until: now + HOUR },
    { id: '4h', label: '4 hours', until: now + 4 * HOUR },
    { id: 'tomorrow', label: 'Tomorrow 09:00', until: localAt(now, 9, 1) },
  ]
}

export function deferPresets(now: number): TimePreset[] {
  const out: TimePreset[] = []
  const afternoon = localAt(now, 15)
  if (afternoon > now + 30 * MINUTE) out.push({ id: 'afternoon', label: 'This afternoon 15:00', until: afternoon })
  out.push({ id: 'tomorrow', label: 'Tomorrow 09:00', until: localAt(now, 9, 1) })
  const d = new Date(now)
  const toMonday = ((8 - d.getDay()) % 7) || 7
  out.push({ id: 'monday', label: 'Next Monday 09:00', until: localAt(now, 9, toMonday) })
  return out
}

/** Defer-to-context options. The wake condition is evaluated server-side (see BACKEND_NEEDS). */
export const DEFER_CONTEXTS = [
  { id: 'agent-idle', label: 'When the agent is idle' },
  { id: 'task-done', label: 'After the linked task closes' },
  { id: 'next-session', label: 'Next working session' },
] as const

/** Until a server evaluates the context, a context defer resurfaces after this fallback. */
export const CONTEXT_DEFER_FALLBACK_MS = 24 * HOUR

// --- approve / deny decision seam ---------------------------------------

export interface AlertDecisionRequest {
  eventId: string
  decision: 'approve' | 'deny'
  note?: string
  /** Opaque IdentityConfirm proof, forwarded for the backend to verify. */
  confirmationToken?: string
  subject?: { type: string; id: string }
}

export interface AlertDecisionClient {
  decide(req: AlertDecisionRequest): Promise<{ ok: boolean; message?: string }>
}

export interface MockDecisionClient extends AlertDecisionClient {
  calls(): AlertDecisionRequest[]
}

export function createMockDecisionClient(opts: { latencyMs?: number; failWith?: string } = {}): MockDecisionClient {
  const log: AlertDecisionRequest[] = []
  return {
    async decide(req) {
      if (opts.latencyMs) await new Promise((r) => setTimeout(r, opts.latencyMs))
      if (opts.failWith) throw new Error(opts.failWith)
      log.push(req)
      return { ok: true, message: req.decision === 'approve' ? 'Approved (mock, no backend yet)' : 'Denied (mock, no backend yet)' }
    },
    calls: () => log,
  }
}

export const ALERT_DECISION_KINDS = { approve: 'alert.approve', deny: 'alert.deny' } as const

/** Registers the approve / deny handlers on the bus. Re-registering the same kinds replaces them (live client swap). */
export function registerAlertDecisionActions(bus: ActionBus, client: AlertDecisionClient): () => void {
  const handler = (decision: 'approve' | 'deny') => async (action: ConsoleAction, ctx: { confirmation?: { token?: string } }) => {
    if (!action.eventId) return { status: 'error' as const, message: 'No event to decide on' }
    const subject = action.subject && { type: action.subject.type, id: action.subject.id }
    const res = await client.decide({
      eventId: action.eventId,
      decision,
      note: typeof action.params?.note === 'string' ? action.params.note : undefined,
      confirmationToken: ctx.confirmation?.token,
      subject,
    })
    return res.ok ? { status: 'ok' as const, message: res.message } : { status: 'error' as const, message: res.message ?? 'Decision rejected' }
  }
  const offs = [bus.register(ALERT_DECISION_KINDS.approve, handler('approve')), bus.register(ALERT_DECISION_KINDS.deny, handler('deny'))]
  return () => offs.forEach((o) => o())
}
