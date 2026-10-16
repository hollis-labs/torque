import type { MetricStatus, Tone } from './types'

/** Theme mapping in one place: swap these classes to retheme the whole kit. */
export const TONE: Record<Tone, { text: string; bg: string; stroke: string }> = {
  neutral: { text: 'text-zinc-300', bg: 'bg-zinc-500', stroke: 'stroke-zinc-400' },
  good: { text: 'text-emerald-300', bg: 'bg-emerald-500', stroke: 'stroke-emerald-400' },
  warn: { text: 'text-amber-300', bg: 'bg-amber-400', stroke: 'stroke-amber-300' },
  bad: { text: 'text-rose-300', bg: 'bg-rose-500', stroke: 'stroke-rose-400' },
  info: { text: 'text-sky-300', bg: 'bg-sky-500', stroke: 'stroke-sky-400' },
}

export const STATUS_TONE: Record<MetricStatus, Tone> = {
  done: 'good',
  in_progress: 'info',
  review: 'warn',
  blocked: 'bad',
  todo: 'neutral',
}

export const STATUS_LABEL: Record<MetricStatus, string> = {
  done: 'Done',
  in_progress: 'In progress',
  review: 'Review',
  blocked: 'Blocked',
  todo: 'To do',
}

export function toneForStatus(status: string): Tone {
  return STATUS_TONE[status as MetricStatus] ?? 'neutral'
}

export function labelForStatus(status: string): string {
  return STATUS_LABEL[status as MetricStatus] ?? status
}
