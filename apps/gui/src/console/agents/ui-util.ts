import type { Risk } from './types'

export const RISK_STYLE: Record<Risk, string> = {
  low: 'border-zinc-700 text-zinc-300',
  medium: 'border-amber-400/50 text-amber-300',
  high: 'border-rose-400/60 text-rose-300',
}

export const clock = (ms: number) => new Date(ms).toISOString().slice(0, 19).replace('T', ' ') + 'Z'
