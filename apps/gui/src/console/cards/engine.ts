// Pure rule engine: events (+ clock) -> cards. Deriving instead of storing keeps
// cards live by construction: a later event with the same key coalesces into the
// card, a resolver event closes it, and a ttl expires it, all from the same
// event list the ConsoleStore already holds.
import { SEVERITY_ORDER, type ConsoleEvent } from '../events/types'
import { renderTemplate } from './template'
import { offerToSpec, type CardRule, type CardRuleSet, type RuleActionDecl, type RuleMatch } from './rules'
import type { ActionIntent, CardActionSpec, CardInstance } from './types'

export function matchesRule(m: RuleMatch, e: ConsoleEvent): boolean {
  if (m.kinds && !m.kinds.includes(e.kind)) return false
  if (m.kindPrefix && !e.kind.startsWith(m.kindPrefix)) return false
  if (m.minSeverity && SEVERITY_ORDER.indexOf(e.severity) < SEVERITY_ORDER.indexOf(m.minSeverity)) return false
  if (m.subjectType && !e.subjects.some((s) => s.type === m.subjectType)) return false
  if (m.payloadEquals) {
    const p = e.payload as Record<string, unknown>
    for (const [k, v] of Object.entries(m.payloadEquals)) if (p?.[k] !== v) return false
  }
  return Boolean(m.kinds || m.kindPrefix || m.subjectType || m.payloadEquals || m.minSeverity)
}

function declToSpec(d: RuleActionDecl, title: string): CardActionSpec {
  return {
    id: d.id,
    label: d.label,
    intent: (d.intent ?? 'neutral') as ActionIntent,
    kind: d.kind,
    params: d.params,
    settlesAs: d.settlesAs,
    origin: 'declared',
    ...(d.confirmScope ? { confirm: { what: { action: `${d.kind}:${d.id}`, title: `${d.label}: ${title}` }, scope: d.confirmScope } } : {}),
  }
}

function buildActions(rule: CardRule, e: ConsoleEvent, title: string): CardActionSpec[] {
  const from = rule.card.actionsFrom ?? 'both'
  const offered = from === 'declared' ? [] : e.actions.map(offerToSpec)
  const declared = from === 'offered' ? [] : (rule.card.actions ?? []).map((d) => declToSpec(d, title))
  // An event's own offer wins over a declared default with the same id.
  // It still inherits the declared default's settlesAs and intent, so the rule owns the card semantics.
  const byId = new Map(declared.map((d) => [d.id, d]))
  const merged = offered.map((o) => {
    const d = byId.get(o.id)
    return d ? { ...o, intent: d.intent, settlesAs: o.settlesAs ?? d.settlesAs } : o
  })
  const ids = new Set(offered.map((a) => a.id))
  return [...merged, ...declared.filter((a) => !ids.has(a.id))].map((a) => ({
    ...a,
    confirm: a.confirm ? { ...a.confirm, what: { ...a.confirm.what, title: a.confirm.what.title || title } } : undefined,
  }))
}

function build(rule: CardRule, e: ConsoleEvent, key: string, prev: CardInstance | undefined): CardInstance {
  const title = renderTemplate(rule.card.title, e) || e.kind
  const exp = rule.card.ttlMs
    ? e.ts + rule.card.ttlMs
    : rule.card.expiresAtPayload && typeof (e.payload as Record<string, unknown>)?.[rule.card.expiresAtPayload] === 'number'
      ? ((e.payload as Record<string, unknown>)[rule.card.expiresAtPayload] as number)
      : undefined
  return {
    id: key,
    kind: rule.card.kind,
    ruleId: rule.id,
    title,
    summary: rule.card.summary ? renderTemplate(rule.card.summary, e) || undefined : undefined,
    severity: rule.card.severity && rule.card.severity !== 'event' ? rule.card.severity : e.severity,
    phase: 'live',
    firstTs: prev?.firstTs ?? e.ts,
    ts: e.ts,
    count: (prev?.count ?? 0) + 1,
    surfaces: rule.card.surfaces,
    subjects: e.subjects,
    event: e,
    actions: buildActions(rule, e, title),
    expiresAt: exp,
    props: rule.card.props ?? {},
  }
}

export interface DeriveOptions {
  now?: number
}

/** `events` must be ascending by (ts, id), as the ConsoleStore keeps them. Result is newest-first by latest ts. */
export function deriveCards(events: ConsoleEvent[], set: CardRuleSet, opts: DeriveOptions = {}): CardInstance[] {
  const now = opts.now ?? Date.now()
  const cards = new Map<string, CardInstance>()
  for (const e of events) {
    const rule = set.rules.find((r) => matchesRule(r.match, e))
    if (rule) {
      const key = renderTemplate(rule.card.dedupeKey, e)
      const prev = cards.get(key)
      if (!prev || prev.phase === 'superseded' || rule.latestWins !== false) {
        // A new event on a closed key reopens it (e.g. a second approval round).
        cards.set(key, build(rule, e, key, prev && prev.phase !== 'superseded' ? prev : undefined))
      } else {
        cards.set(key, { ...prev, count: prev.count + 1 })
      }
      for (const tpl of rule.card.supersedes ?? []) {
        const old = cards.get(renderTemplate(tpl, e))
        if (old && old.id !== key) cards.set(old.id, { ...old, phase: 'superseded' })
      }
    }
    for (const r of set.resolvers ?? []) {
      if (!matchesRule(r.match, e)) continue
      const target = cards.get(renderTemplate(r.key, e))
      if (target) cards.set(target.id, { ...target, phase: 'resolved', resolution: { label: r.label, eventId: e.id, ts: e.ts } })
    }
  }
  const out = [...cards.values()].map((c) => (c.phase === 'live' && c.expiresAt !== undefined && c.expiresAt <= now ? { ...c, phase: 'expired' as const } : c))
  return out.sort((a, b) => b.ts - a.ts || a.id.localeCompare(b.id))
}
