import type { StatusTone } from '../refs'

/** Square, outlined status colours shared by the card and drawer (Operations dashboard style). */
export const TONE: Record<StatusTone, string> = {
  neutral: 'border-zinc-700 text-zinc-300',
  info: 'border-sky-500/50 text-sky-300',
  success: 'border-emerald-500/50 text-emerald-300',
  warning: 'border-amber-500/50 text-amber-300',
  danger: 'border-red-500/50 text-red-300',
}
