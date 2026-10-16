// HITL <-> card pipeline glue. Event kinds are PLACEHOLDERS until the data
// team's stream design lands (approval.requested/resolved, see
// event_message_stream_contract_2026_10_16): they live only in HITL_EVENT_KINDS
// and HITL_CARD_RULES, so remapping needs no component change.
import type { ConsoleEvent, ConsoleSeverity } from '../events/types'
import type { CardRuleSet } from '../cards/rules'
import type { CardInstance, CardActionSpec } from '../cards/types'
import { HITL_TYPES, type HitlRequest, type HitlRisk, type HitlType } from './types'

export const HITL_EVENT_KINDS: Record<HitlType, string> = {
  approval: 'hitl.approval.requested',
  question: 'hitl.question.requested',
  secret: 'hitl.secret.requested',
  form: 'hitl.form.requested',
}
export const HITL_RESOLVED_KIND = 'hitl.resolved'
export const HITL_CARD_KIND: Record<HitlType, string> = {
  approval: 'hitl.approval',
  question: 'hitl.question',
  secret: 'hitl.secret',
  form: 'hitl.form',
}

export const RISK_SEVERITY: Record<HitlRisk, ConsoleSeverity> = { low: 'info', medium: 'notice', high: 'warning', critical: 'error' }

/** ActionBus kinds the HITL handlers register (handlers.ts). */
export const HITL_ACTION = {
  approve: 'hitl.approve',
  reject: 'hitl.reject',
  defer: 'hitl.defer',
  answer: 'hitl.answer',
  form: 'hitl.form.submit',
  secret: 'hitl.secret.submit',
} as const

export function approvalConfirm(req: Pick<HitlRequest, 'id' | 'title'> & { scope?: string[]; prompt?: string }): NonNullable<CardActionSpec['confirm']> {
  return {
    what: { action: 'hitl.approval.approve', title: `Approve: ${req.title}`, summary: req.prompt, subject: { type: 'hitl', id: req.id } },
    scope: ['hitl:approve', ...(req.scope ?? [])],
  }
}

export function approvalActions(req: Extract<HitlRequest, { type: 'approval' }>): CardActionSpec[] {
  const params = { requestId: req.id }
  return [
    { id: 'approve', label: 'Approve', intent: 'primary', kind: HITL_ACTION.approve, params, confirm: approvalConfirm(req), origin: 'declared' },
    { id: 'reject', label: 'Reject', intent: 'danger', kind: HITL_ACTION.reject, params, origin: 'declared' },
    ...(req.allowDefer ? [{ id: 'defer', label: 'Defer', intent: 'neutral' as const, kind: HITL_ACTION.defer, params, origin: 'declared' as const }] : []),
  ]
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown, d = ''): string => (typeof v === 'string' ? v : d)

/** Tolerant parse of an untrusted payload into a HitlRequest; undefined when it is not recognisably one. */
export function parseHitlRequest(raw: unknown): HitlRequest | undefined {
  if (!isObj(raw) || typeof raw.id !== 'string' || !HITL_TYPES.includes(raw.type as HitlType)) return undefined
  const risk = (['low', 'medium', 'high', 'critical'] as const).find((r) => r === raw.risk) ?? 'medium'
  const status = (['pending', 'answered', 'expired', 'withdrawn'] as const).find((s) => s === raw.status) ?? 'pending'
  const requester = isObj(raw.requester) ? { id: str(raw.requester.id, 'unknown'), label: str(raw.requester.label) || undefined, kind: str(raw.requester.kind) || undefined } : { id: 'unknown' }
  const base = {
    id: raw.id,
    title: str(raw.title, '(untitled request)'),
    prompt: str(raw.prompt) || undefined,
    requester,
    risk,
    createdAt: typeof raw.createdAt === 'number' ? raw.createdAt : Date.now(),
    expiresAt: typeof raw.expiresAt === 'number' ? raw.expiresAt : undefined,
    contextLinks: Array.isArray(raw.contextLinks) ? raw.contextLinks.filter((l) => isObj(l) && typeof l.type === 'string' && typeof l.id === 'string') : [],
    status,
    audit: Array.isArray(raw.audit) ? raw.audit.filter((a) => isObj(a) && typeof a.action === 'string') : [],
    outcome: isObj(raw.outcome) && typeof raw.outcome.label === 'string' ? raw.outcome : undefined,
    correlationId: str(raw.correlationId) || undefined,
    tangentKind: str(raw.tangentKind) || undefined,
  } as Omit<HitlRequest, 'type'>
  const arr = <T,>(v: unknown): T[] => (Array.isArray(v) ? (v as T[]) : [])
  switch (raw.type) {
    case 'approval':
      return { ...base, type: 'approval', scope: arr<string>(raw.scope).filter((s) => typeof s === 'string'), effects: arr<string>(raw.effects).filter((s) => typeof s === 'string'), allowDefer: raw.allowDefer === true }
    case 'question':
      return { ...base, type: 'question', mode: raw.mode === 'multi' || raw.mode === 'text' ? raw.mode : 'choice', options: arr<HitlRequest & { value: string }>(raw.options).filter((o) => isObj(o) && typeof o.value === 'string') as never, allowOther: raw.allowOther === true, placeholder: str(raw.placeholder) || undefined }
    case 'secret':
      return { ...base, type: 'secret', fieldLabel: str(raw.fieldLabel, 'Secret'), purpose: str(raw.purpose), destination: str(raw.destination) || undefined }
    default:
      return { ...base, type: 'form', fields: arr<never>(raw.fields).filter((f) => isObj(f) && typeof (f as Record<string, unknown>).id === 'string') as never, submitLabel: str(raw.submitLabel) || undefined }
  }
}

/** The synthetic event a request is presented as. Payload IS the request, so the same components render event-derived cards. */
export function requestToEvent(req: HitlRequest): ConsoleEvent {
  return {
    id: `hitl-${req.id}`,
    ts: req.createdAt,
    kind: HITL_EVENT_KINDS[req.type],
    source: { id: 'hitl' },
    subjects: [{ type: 'hitl', id: req.id, label: req.title }, ...req.contextLinks],
    severity: RISK_SEVERITY[req.risk],
    payload: req as unknown as Record<string, unknown>,
    correlationId: req.correlationId ?? req.id,
    actions: [],
    extra: {},
  }
}

/** Build a manual CardInstance (CardHost.present) from a request. `now` decides local expiry. */
export function requestToCard(req: HitlRequest, now = Date.now()): CardInstance {
  const expired = req.status === 'pending' && req.expiresAt !== undefined && req.expiresAt <= now
  const phase: CardInstance['phase'] = req.status === 'answered' ? 'resolved' : req.status === 'withdrawn' ? 'superseded' : req.status === 'expired' || expired ? 'expired' : 'live'
  const last = req.audit.length ? req.audit[req.audit.length - 1].at : req.createdAt
  return {
    id: `hitl:${req.id}`,
    kind: HITL_CARD_KIND[req.type],
    ruleId: 'hitl',
    title: req.title,
    summary: req.prompt,
    severity: RISK_SEVERITY[req.risk],
    phase,
    firstTs: req.createdAt,
    ts: last,
    count: 1,
    surfaces: req.type === 'secret' ? ['tray', 'modal', 'toast'] : req.type === 'form' ? ['tray', 'modal', 'inline'] : ['tray', 'modal', 'inline', 'toast'],
    subjects: [{ type: 'hitl', id: req.id, label: req.title }, ...req.contextLinks],
    event: requestToEvent(req),
    actions: req.type === 'approval' ? approvalActions(req) : [],
    expiresAt: req.expiresAt,
    ...(phase === 'resolved' ? { resolution: { label: req.outcome?.label ?? 'Answered', ts: req.outcome?.at ?? last } } : {}),
    props: { request: req },
  }
}

/** Read the request back out of a card: props first (presented), else the event payload (rule-derived). */
export function requestFromCard(card: CardInstance): HitlRequest | undefined {
  return parseHitlRequest(card.props.request) ?? parseHitlRequest(card.event.payload)
}

/**
 * Optional event-driven path: merge into the CardRuleSet to derive HITL cards
 * straight from the feed instead of presenting them from a HitlClient. Swap the
 * kinds in HITL_EVENT_KINDS when the stream design lands.
 */
export const HITL_CARD_RULES: CardRuleSet = {
  rules: HITL_TYPES.map((t) => ({
    id: `hitl-${t}`,
    match: { kinds: [HITL_EVENT_KINDS[t]] },
    card: {
      kind: HITL_CARD_KIND[t],
      surfaces: t === 'secret' ? ['tray', 'modal', 'toast'] : t === 'form' ? ['tray', 'modal', 'inline'] : ['tray', 'modal', 'inline', 'toast'],
      dedupeKey: 'hitl:{payload.id}',
      title: '{payload.title}',
      summary: '{payload.prompt}',
      expiresAtPayload: 'expiresAt',
      actionsFrom: 'declared',
    },
  })),
  resolvers: [{ id: 'hitl-resolved', match: { kinds: [HITL_RESOLVED_KIND] }, key: 'hitl:{payload.id}', label: 'Resolved' }],
}

export function withHitlRules(base: CardRuleSet): CardRuleSet {
  return { rules: [...base.rules, ...HITL_CARD_RULES.rules], resolvers: [...(base.resolvers ?? []), ...(HITL_CARD_RULES.resolvers ?? [])] }
}

const MIN = 60_000
/** "2h 05m", "4m 12s", "38s"; "expired" at or past zero. */
export function fmtRemaining(ms: number): string {
  if (ms <= 0) return 'expired'
  const s = Math.ceil(ms / 1000)
  if (s >= 3600) return `${Math.floor(s / 3600)}h ${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m`
  if (s >= MIN / 1000) return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`
  return `${s}s`
}
