// Messaging projection helpers.
//
// Torque's `/api/v1/messages/*` routes return raw go-messaging Envelopes —
// there is no server-side projection (unlike Tether, whose backend folds
// payload fields into subject/body). The messaging GUI therefore derives
// its display fields client-side, here, so the projection stays in one place.

import type { MessageEnvelope } from './types'

export interface ParsedUrn {
  kind: string
  authority: string
  id: string
  subId?: string
}

/** Parse a canonical messaging URN `msg://<kind>/<authority>/<id>[/<subid>]`. */
export function parseUrn(urn: string): ParsedUrn | null {
  const m = /^msg:\/\/([^/]+)\/([^/]+)\/([^/]+)(?:\/([^/]+))?$/.exec((urn ?? '').trim())
  if (!m) return null
  return { kind: m[1], authority: m[2], id: m[3], subId: m[4] }
}

/** Strip the `msg://<kind>/` prefix to a readable `authority/id[/sub]` tail. */
export function shortUrn(urn: string): string {
  const p = parseUrn(urn)
  if (!p) return urn
  const tail = p.subId ? `${p.id}/${p.subId}` : p.id
  return `${p.authority}/${tail}`
}

export type MessageScope = 'user' | 'agent'

/**
 * User vs agent scope, derived from the recipient address kind. Anything
 * not addressed to a `user` URN is treated as agent-to-agent traffic so the
 * agent tab is a catch-all an operator can monitor.
 */
export function messageScope(m: MessageEnvelope): MessageScope {
  return parseUrn(m.to)?.kind === 'user' ? 'user' : 'agent'
}

const SUBJECT_KEYS = ['subject', 'title']
const BODY_KEYS = ['body', 'text', 'message', 'summary']

/** Payload keys already surfaced as subject/body — excluded from "Details". */
export const PROJECTED_PAYLOAD_KEYS = new Set([...SUBJECT_KEYS, ...BODY_KEYS])

function payloadObject(payload: unknown): Record<string, unknown> | null {
  if (payload && typeof payload === 'object' && !Array.isArray(payload)) {
    return payload as Record<string, unknown>
  }
  if (typeof payload === 'string') {
    try {
      const o: unknown = JSON.parse(payload)
      return o && typeof o === 'object' && !Array.isArray(o)
        ? (o as Record<string, unknown>)
        : null
    } catch {
      return null
    }
  }
  return null
}

/** One-line headline — payload subject/title, else first non-empty body line. */
export function messageSubject(m: MessageEnvelope): string {
  const o = payloadObject(m.payload)
  if (o) {
    for (const k of SUBJECT_KEYS) {
      const v = o[k]
      if (typeof v === 'string' && v.trim()) return v
    }
  }
  return messageBody(m).split('\n').find((l) => l.trim()) ?? ''
}

/** Human-readable message text — payload body/text/message/summary or raw. */
export function messageBody(m: MessageEnvelope): string {
  const o = payloadObject(m.payload)
  if (o) {
    for (const k of BODY_KEYS) {
      const v = o[k]
      if (typeof v === 'string' && v.trim()) return v
    }
  }
  if (typeof m.payload === 'string') return m.payload
  if (m.payload == null) return ''
  try {
    return JSON.stringify(m.payload, null, 2)
  } catch {
    return String(m.payload)
  }
}

/** Structured payload fields not already shown as subject/body. */
export function extraPayloadFields(m: MessageEnvelope): [string, unknown][] {
  const o = payloadObject(m.payload)
  if (!o) return []
  return Object.entries(o).filter(([k]) => !PROJECTED_PAYLOAD_KEYS.has(k))
}

export type MessageStatus = 'pending' | 'delivered' | 'consumed'

/** Lifecycle status — most-advanced state wins. */
export function messageStatus(m: MessageEnvelope): MessageStatus {
  if (m.consumed_at) return 'consumed'
  if (m.delivered_at) return 'delivered'
  return 'pending'
}

/** Compact relative time, e.g. "just now", "4m ago", "2h ago", "3d ago". */
export function formatRelativeTime(iso: string): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const diff = Date.now() - t
  if (diff < 0) return 'just now'
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

/** Stable thread key for an envelope — explicit thread_id, else its own id. */
export function threadKey(m: MessageEnvelope): string {
  return m.thread_id || m.id
}

/**
 * The "other party" in a message relative to `self` — the correspondent the
 * conversation rollup groups by. Outbound messages (self → x) key on `to`;
 * inbound messages (x → self) key on `from`. Both sides are trimmed so stray
 * whitespace cannot split one correspondent into two conversations.
 *
 * Note: this is deliberately NOT `threadKey`. Many envelopes carry an empty
 * `thread_id` (migration 022 defaults it to ''), so grouping by thread would
 * explode into per-message singletons; grouping by correspondent gives the
 * one-row-per-session/agent rollup the messaging view wants.
 */
export function correspondentKey(m: MessageEnvelope, self: string): string {
  const me = (self ?? '').trim()
  return m.from.trim() === me ? m.to.trim() : m.from.trim()
}
