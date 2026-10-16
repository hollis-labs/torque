import { SEVERITY_ORDER, type ConsoleEvent } from '../events/types'
import { linkOf, sourceOf } from '../alerts/mapping'
import type { Alert, AlertMapper } from '../alerts/types'
import type { AlertRule, AlertRulePack, PayloadCondition, AlertRuleMatch } from './types'

const sevIdx = (s: string) => SEVERITY_ORDER.indexOf(s as never)

function getPath(obj: unknown, path: string): unknown {
  let cur: unknown = obj
  for (const part of path.split('.')) {
    if (cur && typeof cur === 'object' && Object.prototype.hasOwnProperty.call(cur, part)) cur = (cur as Record<string, unknown>)[part]
    else return undefined
  }
  return cur
}

function condHolds(c: PayloadCondition, v: unknown): boolean {
  if (typeof c !== 'object') return v === c
  if (c.exists !== undefined && (v !== undefined && v !== null) !== c.exists) return false
  if (c.eq !== undefined && v !== c.eq) return false
  if (c.in !== undefined && !c.in.includes(v as never)) return false
  if (c.gte !== undefined && !(typeof v === 'number' && v >= c.gte)) return false
  if (c.lte !== undefined && !(typeof v === 'number' && v <= c.lte)) return false
  return true
}

export function kindMatches(pattern: string, kind: string): boolean {
  return pattern.endsWith('.*') ? kind.startsWith(pattern.slice(0, -1)) : pattern === kind
}

export function matchesAlertRule(m: AlertRuleMatch, e: ConsoleEvent): boolean {
  if (m.kinds && !m.kinds.some((k) => kindMatches(k, e.kind))) return false
  if (m.sources && !m.sources.includes(e.source.id)) return false
  if (m.minSeverity && sevIdx(e.severity) < sevIdx(m.minSeverity)) return false
  if (m.severities && !m.severities.includes(e.severity)) return false
  if (m.categories && !(e.class?.category && m.categories.includes(e.class.category))) return false
  if (m.labels && !m.labels.some((l) => e.class?.labels?.includes(l))) return false
  if (m.subjectTypes && !e.subjects.some((s) => m.subjectTypes!.includes(s.type))) return false
  if (m.payload) for (const [path, c] of Object.entries(m.payload)) if (!condHolds(c, getPath(e.payload, path))) return false
  return true
}

function templateScope(e: ConsoleEvent) {
  const agent = e.subjects.find((s) => s.type === 'agent')
  const subj = e.subjects.find((s) => s.type !== 'agent') ?? e.subjects[0]
  return {
    payload: e.payload,
    subject: { label: subj?.label ?? subj?.id ?? '', id: subj?.id ?? '', type: subj?.type ?? '' },
    agent: agent?.label ?? agent?.id ?? '',
    source: sourceOf(e),
    kind: e.kind,
    severity: e.severity,
  } as Record<string, unknown>
}

/**
 * `{{path}}`, `{{a,b}}` (first non-empty path) and `{{path|literal fallback}}`
 * substitution over a closed scope. No code execution; non-scalars render empty.
 */
export function renderAlertTemplate(tpl: string, e: ConsoleEvent): string {
  const scope = templateScope(e)
  return tpl
    .replace(/\{\{\s*([\w.,\s]+?)\s*(?:\|([^{}]*))?\}\}/g, (_m, paths: string, fallback?: string) => {
      for (const path of paths.split(',')) {
        const v = getPath(scope, path.trim())
        const s = typeof v === 'string' ? v.trim() : typeof v === 'number' || typeof v === 'boolean' ? String(v) : ''
        if (s) return s
      }
      return (fallback ?? '').trim()
    })
    .replace(/\s+/g, ' ')
    .trim()
}

const oneLine = (v: string, max = 140) => (v.length > max ? `${v.slice(0, max - 1)}…` : v)

function genericIcon(sev: string): Alert['icon'] {
  return sev === 'error' || sev === 'critical' ? 'error' : sev === 'warning' ? 'warning' : 'info'
}

export function ruleToAlert(rule: AlertRule, e: ConsoleEvent): Alert | null {
  if (rule.action === 'drop' || !rule.alert) return null
  const a = rule.alert
  const severity = !a.severity || a.severity === 'inherit' ? e.severity : a.severity
  const out: Alert = {
    id: e.id,
    threadId: e.threadId,
    severity,
    icon: a.icon ?? genericIcon(severity),
    title: oneLine(renderAlertTemplate(a.title, e)) || e.kind,
    detail: a.detail ? oneLine(renderAlertTemplate(a.detail, e)) || undefined : undefined,
    source: sourceOf(e),
    ts: e.ts,
    kind: e.kind,
    primary: linkOf(e),
    ruleId: rule.id,
  }
  if (rule.coalesce) {
    const key = renderAlertTemplate(rule.coalesce.key, e)
    if (key) {
      out.coalesceKey = key
      if (rule.coalesce.windowSec !== undefined) out.coalesceWindowMs = rule.coalesce.windowSec * 1000
    }
  }
  if (rule.escalate && sevIdx(rule.escalate.to) > sevIdx(severity)) out.escalate = { afterMs: rule.escalate.afterSec * 1000, to: rule.escalate.to }
  return out
}

/** Build an AlertMapper from a pack: first enabled matching rule wins; no match = not an alert. */
export function createRuleMapper(pack: AlertRulePack | AlertRule[]): AlertMapper {
  const rules = (Array.isArray(pack) ? pack : pack.rules).filter((r) => r.enabled !== false)
  return (e) => {
    const rule = rules.find((r) => matchesAlertRule(r.match, e))
    return rule ? ruleToAlert(rule, e) : null
  }
}
