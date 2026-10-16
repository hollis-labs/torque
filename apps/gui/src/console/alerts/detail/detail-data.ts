// Pure view-model helpers for the alert detail: structured fields and related events.
import type { ConsoleEvent } from '../../events/types'

export interface DetailField {
  label: string
  value: string
}

const SKIP = new Set(['title', 'summary', 'preview', 'subject'])
const clip = (s: string, n = 300) => (s.length > n ? `${s.slice(0, n - 1)}…` : s)

/** Scalar payload entries plus envelope facts. Event text is untrusted: values are rendered as text only. */
export function structuredFields(e: ConsoleEvent | undefined): DetailField[] {
  if (!e) return []
  const out: DetailField[] = []
  for (const [k, v] of Object.entries(e.payload as Record<string, unknown>)) {
    if (SKIP.has(k)) continue
    if (typeof v === 'string' && v.trim()) out.push({ label: k, value: clip(v.trim()) })
    else if (typeof v === 'number' || typeof v === 'boolean') out.push({ label: k, value: String(v) })
  }
  out.push({ label: 'source', value: e.source.machine ? `${e.source.id} on ${e.source.machine}` : e.source.id })
  if (e.threadId) out.push({ label: 'thread', value: e.threadId })
  if (e.correlationId) out.push({ label: 'correlation', value: e.correlationId })
  out.push({ label: 'event', value: e.id })
  return out
}

/** Other events on the same thread / correlation, oldest first, max `limit` (the most recent ones). */
export function relatedEvents(all: ConsoleEvent[], e: ConsoleEvent | undefined, limit = 8): ConsoleEvent[] {
  if (!e || (!e.threadId && !e.correlationId)) return []
  const rel = all.filter((x) => x.id !== e.id && ((e.threadId && x.threadId === e.threadId) || (e.correlationId && x.correlationId === e.correlationId)))
  return rel.sort((a, b) => a.ts - b.ts).slice(-limit)
}

export function replyTarget(alert: { source: string }, event?: ConsoleEvent): string | null {
  const p = (event?.payload ?? {}) as Record<string, unknown>
  const s = (v: unknown) => (typeof v === 'string' && v.trim() ? v.trim() : null)
  return s(p.fromAddress) ?? s(p.from) ?? event?.subjects.find((x) => x.type === 'agent')?.id ?? s(alert.source.split(' · ')[0])
}

