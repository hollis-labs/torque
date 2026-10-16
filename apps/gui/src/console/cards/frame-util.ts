import type { ConsoleSeverity } from '../events/types'

export const CARD_SEVERITY_STYLE: Record<ConsoleSeverity, { rail: string; text: string; label: string }> = {
  debug: { rail: 'bg-zinc-700', text: 'text-zinc-500', label: 'DEBUG' },
  info: { rail: 'bg-sky-500', text: 'text-sky-300', label: 'INFO' },
  notice: { rail: 'bg-emerald-500', text: 'text-emerald-300', label: 'NOTICE' },
  warning: { rail: 'bg-amber-400', text: 'text-amber-300', label: 'WARNING' },
  error: { rail: 'bg-red-500', text: 'text-red-300', label: 'ERROR' },
  critical: { rail: 'bg-red-400', text: 'text-red-200', label: 'CRITICAL' },
}

export function relTime(ts: number, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - ts) / 1000))
  if (s < 45) return 'now'
  if (s < 3600) return `${Math.round(s / 60)}m`
  if (s < 86_400) return `${Math.round(s / 3600)}h`
  return `${Math.round(s / 86_400)}d`
}

