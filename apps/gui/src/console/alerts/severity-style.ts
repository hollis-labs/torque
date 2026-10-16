import type { AlertSeverity } from './types'

export interface SeverityStyle {
  label: string
  /** Left stripe + dot. */
  bar: string
  text: string
  badge: string
}

export const SEVERITY_STYLE: Record<AlertSeverity, SeverityStyle> = {
  debug: { label: 'DEBUG', bar: 'bg-zinc-600', text: 'text-zinc-400', badge: 'bg-zinc-500 text-zinc-950' },
  info: { label: 'INFO', bar: 'bg-sky-400', text: 'text-sky-300', badge: 'bg-sky-400 text-zinc-950' },
  notice: { label: 'NOTICE', bar: 'bg-emerald-400', text: 'text-emerald-300', badge: 'bg-emerald-400 text-zinc-950' },
  warning: { label: 'WARNING', bar: 'bg-amber-400', text: 'text-amber-300', badge: 'bg-amber-400 text-zinc-950' },
  error: { label: 'ERROR', bar: 'bg-red-400', text: 'text-red-300', badge: 'bg-red-500 text-white' },
  critical: { label: 'CRITICAL', bar: 'bg-red-500', text: 'text-red-300', badge: 'bg-red-500 text-white' },
}
