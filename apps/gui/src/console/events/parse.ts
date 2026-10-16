import type { ConsoleActionOffer, ConsoleActor, ConsoleClass, ConsoleCorr, ConsoleEvent, ConsoleRef, ConsoleScope, ConsoleSeverity, GapInfo } from './types'
import { SEVERITY_ORDER } from './types'

const MAX_TEXT = 500
const KNOWN_TOP = new Set([
  'id', 'ts', 'time', 'kind', 'source', 'subjects', 'subject', 'severity', 'payload',
  'correlation_id', 'correlationId', 'thread_id', 'threadId', 'actions',
  // v1.1 typed blocks (now first-class fields, so not "extra")
  'scope', 'corr', 'actor', 'class', 'recorded_at', 'recordedAt', 'epoch', 'cursor',
])

function text(v: unknown): string {
  return typeof v === 'string' ? v.slice(0, MAX_TEXT) : ''
}

function obj(v: unknown): Record<string, unknown> | null {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null
}

function when(v: unknown): number | null {
  if (typeof v === 'number' && Number.isFinite(v)) return v
  if (typeof v === 'string') {
    const t = Date.parse(v)
    return Number.isNaN(t) ? null : t
  }
  return null
}

function parseRef(v: unknown): ConsoleRef | null {
  const o = obj(v)
  if (!o) return null
  const type = text(o.type)
  const id = text(o.id)
  if (!type || !id) return null
  return { type, id, ...(text(o.label) ? { label: text(o.label) } : {}), ...(text(o.url) ? { url: text(o.url) } : {}) }
}

function pick(o: Record<string, unknown>, snake: string, camel: string): unknown {
  return o[camel] ?? o[snake]
}

function parseScope(v: unknown): ConsoleScope | undefined {
  const o = obj(v)
  if (!o) return undefined
  const out: ConsoleScope = {}
  for (const [k, val] of Object.entries(o)) {
    if (typeof val === 'string') out[k] = val.slice(0, MAX_TEXT)
    else if (typeof val === 'number' && Number.isFinite(val)) out[k] = val
  }
  return Object.keys(out).length ? out : undefined
}

function parseCorr(v: unknown): ConsoleCorr | undefined {
  const o = obj(v)
  if (!o) return undefined
  const out: ConsoleCorr = {}
  const keys: [keyof ConsoleCorr, string][] = [
    ['traceId', 'trace_id'], ['spanId', 'span_id'], ['causationId', 'causation_id'],
    ['correlationId', 'correlation_id'], ['threadId', 'thread_id'], ['idempotencyKey', 'idempotency_key'],
  ]
  for (const [camel, snake] of keys) {
    const s = text(pick(o, snake, camel))
    if (s) (out as Record<string, unknown>)[camel] = s
  }
  if (typeof o.depth === 'number' && Number.isFinite(o.depth)) out.depth = o.depth
  return Object.keys(out).length ? out : undefined
}

function parseActor(v: unknown): ConsoleActor | undefined {
  const o = obj(v)
  if (!o) return undefined
  const kind = text(o.kind)
  if (!kind) return undefined
  const id = text(o.id)
  const obo = text(pick(o, 'on_behalf_of', 'onBehalfOf'))
  const verified = text(o.verified)
  return { kind, ...(id ? { id } : {}), ...(obo ? { onBehalfOf: obo } : {}), ...(verified ? { verified } : {}) }
}

function parseClass(v: unknown): ConsoleClass | undefined {
  const o = obj(v)
  if (!o) return undefined
  const out: ConsoleClass = {}
  const category = text(o.category)
  if (category) out.category = category
  if (o.severity !== undefined) out.severity = parseSeverity(o.severity)
  const sens = text(o.sensitivity)
  if (sens) out.sensitivity = sens
  if (o.pii === true || sens === 'pii') out.pii = true
  const vis = text(o.visibility)
  if (vis) out.visibility = vis
  const schema = text(pick(o, 'payload_schema', 'payloadSchema'))
  if (schema) out.payloadSchema = schema
  if (Array.isArray(o.labels)) out.labels = o.labels.map(text).filter(Boolean)
  if (typeof o.sanitized === 'boolean') out.sanitized = o.sanitized
  if (typeof o.redactions === 'number') out.redactions = o.redactions
  return Object.keys(out).length ? out : undefined
}

/** Parses a gap frame body (DRAFT: reason, earliest_available, high_water). Tolerates empty/odd input. */
export function parseGapInfo(v: unknown): GapInfo {
  const o = obj(v)
  if (!o) return {}
  const reason = text(o.reason)
  const earliest = text(pick(o, 'earliest_available', 'earliestAvailable'))
  const high = text(pick(o, 'high_water', 'highWater'))
  return { ...(reason ? { reason } : {}), ...(earliest ? { earliestAvailable: earliest } : {}), ...(high ? { highWater: high } : {}) }
}

function parseAction(v: unknown): ConsoleActionOffer | null {
  const o = obj(v)
  if (!o) return null
  const id = text(o.id)
  const kind = text(o.kind)
  if (!id || !kind) return null
  const scope = Array.isArray(o.scope) ? o.scope.map(text).filter(Boolean) : undefined
  return {
    id,
    label: text(o.label) || id,
    kind,
    ...(o.requiresConfirm === true || o.requires_confirm === true ? { requiresConfirm: true } : {}),
    ...(scope && scope.length ? { scope } : {}),
    ...(obj(o.params) ? { params: obj(o.params)! } : {}),
  }
}

function parseSeverity(v: unknown): ConsoleSeverity {
  const s = typeof v === 'string' ? (v.toLowerCase() === 'warn' ? 'warning' : v.toLowerCase()) : ''
  return (SEVERITY_ORDER as readonly string[]).includes(s) ? (s as ConsoleSeverity) : 'info'
}

export interface ParseDefaults {
  /** Used when the raw event has no source. */
  source?: { id: string; machine?: string }
  /** Used when the raw event has no timestamp. */
  now?: () => number
}

/**
 * Tolerant parser. Returns null only when the input cannot be an event at all
 * (not an object, or no usable id and kind). Everything else degrades to defaults.
 */
export function parseConsoleEvent(raw: unknown, defaults: ParseDefaults = {}): ConsoleEvent | null {
  const o = obj(raw)
  if (!o) return null
  const id = text(o.id)
  const kind = text(o.kind)
  if (!id || !kind) return null
  const src = obj(o.source)
  const srcId = text(src?.id) || (typeof o.source === 'string' ? text(o.source) : '') || defaults.source?.id || 'unknown'
  const machine = text(src?.machine) || defaults.source?.machine
  const subjectsRaw = Array.isArray(o.subjects) ? o.subjects : o.subject !== undefined ? [o.subject] : []
  const extra: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(o)) if (!KNOWN_TOP.has(k)) extra[k] = v
  const scope = parseScope(o.scope)
  const corrBlock = parseCorr(o.corr)
  const actor = parseActor(o.actor)
  const cls = parseClass(o.class)
  const corr = text(o.correlationId ?? o.correlation_id) || corrBlock?.correlationId || ''
  const thread = text(o.threadId ?? o.thread_id) || corrBlock?.threadId || ''
  const sessionId = text(pick(o, 'session_id', 'sessionId'))
  const primary = parseRef(o.subject)
  const recordedAt = when(pick(o, 'recorded_at', 'recordedAt'))
  const epoch = o.epoch !== undefined && o.epoch !== null ? String(o.epoch).slice(0, MAX_TEXT) : ''
  const cursor = o.cursor !== undefined && o.cursor !== null ? String(o.cursor).slice(0, MAX_TEXT) : ''
  // Top-level severity wins; class.severity is the v1.1 home for it.
  const severity = o.severity !== undefined ? parseSeverity(o.severity) : (cls?.severity ?? 'info')
  return {
    id,
    ts: when(o.ts ?? o.time) ?? (defaults.now ?? Date.now)(),
    kind,
    source: { id: srcId, ...(machine ? { machine } : {}) },
    subjects: subjectsRaw.map(parseRef).filter((r): r is ConsoleRef => r !== null),
    severity,
    payload: obj(o.payload) ?? {},
    ...(corr ? { correlationId: corr } : {}),
    ...(thread ? { threadId: thread } : {}),
    actions: (Array.isArray(o.actions) ? o.actions : []).map(parseAction).filter((a): a is ConsoleActionOffer => a !== null),
    extra,
    ...(sessionId ? { sessionId } : {}),
    ...(primary ? { subject: primary } : {}),
    ...(scope ? { scope } : {}),
    ...(corrBlock ? { corr: corrBlock } : {}),
    ...(actor ? { actor } : {}),
    ...(cls ? { class: cls } : {}),
    ...(recordedAt !== null ? { recordedAt } : {}),
    ...(epoch ? { epoch } : {}),
    ...(cursor ? { cursor } : {}),
  }
}

export function parseConsoleEvents(raw: unknown, defaults?: ParseDefaults): ConsoleEvent[] {
  if (!Array.isArray(raw)) return []
  return raw.map((r) => parseConsoleEvent(r, defaults)).filter((e): e is ConsoleEvent => e !== null)
}
