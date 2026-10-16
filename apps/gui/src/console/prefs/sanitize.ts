import { SEVERITY_ORDER } from '../events/types'
import { DEFAULT_CONSOLE_PREFS, type ConsolePrefs } from './types'

const D = DEFAULT_CONSOLE_PREFS
const HHMM = /^([01]\d|2[0-3]):[0-5]\d$/

/** Total: any junk input yields a valid ConsolePrefs (stored values are untrusted). */
export function sanitizePrefs(raw: unknown): ConsolePrefs {
  const o = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
  const win = typeof o.coalesceWindowSec === 'number' && Number.isFinite(o.coalesceWindowSec) ? Math.round(o.coalesceWindowSec) : D.coalesceWindowSec
  return {
    quiet: typeof o.quiet === 'boolean' ? o.quiet : D.quiet,
    toastMinSeverity: SEVERITY_ORDER.includes(o.toastMinSeverity as ConsolePrefs['toastMinSeverity']) ? (o.toastMinSeverity as ConsolePrefs['toastMinSeverity']) : D.toastMinSeverity,
    quietHoursEnabled: typeof o.quietHoursEnabled === 'boolean' ? o.quietHoursEnabled : D.quietHoursEnabled,
    quietHoursStart: typeof o.quietHoursStart === 'string' && HHMM.test(o.quietHoursStart) ? o.quietHoursStart : D.quietHoursStart,
    quietHoursEnd: typeof o.quietHoursEnd === 'string' && HHMM.test(o.quietHoursEnd) ? o.quietHoursEnd : D.quietHoursEnd,
    mutedSources: Array.isArray(o.mutedSources)
      ? [...new Set(o.mutedSources.filter((s): s is string => typeof s === 'string').map((s) => s.trim().toLowerCase()).filter(Boolean))].slice(0, 50)
      : D.mutedSources,
    coalesceWindowSec: Math.min(3600, Math.max(0, win)),
  }
}

const minutes = (hhmm: string): number => Number(hhmm.slice(0, 2)) * 60 + Number(hhmm.slice(3, 5))

/** True while the scheduled quiet window is open at `now` (local time). Start == end means never. */
export function inQuietHours(prefs: Pick<ConsolePrefs, 'quietHoursEnabled' | 'quietHoursStart' | 'quietHoursEnd'>, now: number | Date = Date.now()): boolean {
  if (!prefs.quietHoursEnabled) return false
  const d = typeof now === 'number' ? new Date(now) : now
  const cur = d.getHours() * 60 + d.getMinutes()
  const s = minutes(prefs.quietHoursStart)
  const e = minutes(prefs.quietHoursEnd)
  if (s === e) return false
  return s < e ? cur >= s && cur < e : cur >= s || cur < e
}

/** Quiet mode OR scheduled quiet hours. Critical alerts ignore this (see ToastQueue). */
export function isQuietNow(prefs: ConsolePrefs, now: number | Date = Date.now()): boolean {
  return prefs.quiet || inQuietHours(prefs, now)
}

export function isMutedSource(prefs: Pick<ConsolePrefs, 'mutedSources'>, source: string): boolean {
  const s = source.toLowerCase()
  return prefs.mutedSources.some((m) => s.includes(m))
}
