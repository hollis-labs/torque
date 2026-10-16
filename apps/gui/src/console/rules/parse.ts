import { SEVERITY_ORDER } from '../events/types'
import type { AlertRule, AlertRulePack } from './types'

const ICONS = ['info', 'task', 'agent', 'message', 'approval', 'pr', 'warning', 'error']
const isStr = (v: unknown): v is string => typeof v === 'string' && v.length > 0
const isSev = (v: unknown) => SEVERITY_ORDER.includes(v as never)
const strList = (v: unknown): string[] | undefined => (Array.isArray(v) && v.every(isStr) ? (v as string[]) : undefined)

export interface PackParseResult {
  pack: AlertRulePack
  /** Human-readable reasons rules were skipped; empty when everything parsed. */
  problems: string[]
}

/**
 * Validate an untrusted pack (JSON from an agent / server). Invalid rules are
 * skipped with a reason rather than failing the whole pack; unknown fields are ignored.
 */
export function parseRulePack(raw: unknown): PackParseResult {
  const problems: string[] = []
  const o = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
  if (o.version !== 1) problems.push(`unsupported pack version ${String(o.version)}`)
  const rules: AlertRule[] = []
  const seen = new Set<string>()
  if (o.version === 1 && Array.isArray(o.rules)) {
    for (const [i, r] of o.rules.entries()) {
      const x = (r && typeof r === 'object' ? r : {}) as Record<string, unknown>
      const id = isStr(x.id) ? x.id : `#${i}`
      const bad = (why: string) => problems.push(`rule ${id}: ${why}`)
      if (!isStr(x.id)) { bad('missing id'); continue }
      if (seen.has(x.id)) { bad('duplicate id'); continue }
      if (!x.match || typeof x.match !== 'object') { bad('missing match'); continue }
      const m = x.match as Record<string, unknown>
      if (m.kinds !== undefined && !strList(m.kinds)) { bad('match.kinds must be strings'); continue }
      if (m.minSeverity !== undefined && !isSev(m.minSeverity)) { bad('bad match.minSeverity'); continue }
      if (x.action === 'drop') {
        seen.add(x.id)
        rules.push(x as unknown as AlertRule)
        continue
      }
      const a = x.alert as Record<string, unknown> | undefined
      if (!a || !isStr(a.title)) { bad('alert.title required'); continue }
      if (a.severity !== undefined && a.severity !== 'inherit' && !isSev(a.severity)) { bad('bad alert.severity'); continue }
      if (a.icon !== undefined && !ICONS.includes(a.icon as string)) { bad('bad alert.icon'); continue }
      const c = x.coalesce as Record<string, unknown> | undefined
      if (c !== undefined && !isStr(c?.key)) { bad('coalesce.key required'); continue }
      const e = x.escalate as Record<string, unknown> | undefined
      if (e !== undefined && !(typeof e?.afterSec === 'number' && e.afterSec > 0 && isSev(e.to))) { bad('bad escalate'); continue }
      seen.add(x.id)
      rules.push(x as unknown as AlertRule)
    }
  }
  return { pack: { version: 1, id: isStr(o.id) ? o.id : 'unnamed', label: isStr(o.label) ? o.label : undefined, rules }, problems }
}
