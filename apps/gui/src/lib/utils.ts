import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

import type { TaskCostSource } from './types'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Format a date string as relative time (e.g. "3m ago", "2h ago", "5d ago")
 */
export function formatRelativeTime(dateStr: string): string {
  const now = Date.now()
  const then = new Date(dateStr).getTime()
  const diffMs = now - then
  const diffSec = Math.floor(diffMs / 1000)
  const diffMin = Math.floor(diffSec / 60)
  const diffHr = Math.floor(diffMin / 60)
  const diffDay = Math.floor(diffHr / 24)

  if (diffSec < 60) return `${diffSec}s ago`
  if (diffMin < 60) return `${diffMin}m ago`
  if (diffHr < 24) return `${diffHr}h ago`
  if (diffDay < 30) return `${diffDay}d ago`
  return new Date(dateStr).toLocaleDateString()
}

/**
 * Format a cost value in dollars (e.g. "$0.0042"). Source-aware: when the
 * caller knows the cost came from `unknown` or no ledger rows exist
 * (`''`), pass `source` so the function can render `—` instead of a
 * misleading `$0.00`. Measured-and-zero is a legitimate result and still
 * renders `$0.00` — the badge is what disambiguates.
 *
 * `estimated` cost prepends `~` so the figure reads as approximate at a
 * glance even without the surrounding badge — useful in dense table rows
 * where the badge gets stripped for space.
 */
export function formatCost(cost: number, source?: TaskCostSource): string {
  if (source === 'unknown' || source === '') return '—'
  const prefix = source === 'estimated' ? '~$' : '$'
  if (cost === 0) return `${prefix}0.00`
  if (cost < 0.01) return `${prefix}${cost.toFixed(4)}`
  return `${prefix}${cost.toFixed(2)}`
}

// Re-export so callers that already import TaskCostSource from this
// module continue to work without a path change.
export type { TaskCostSource }

/**
 * Format a duration in milliseconds as human-readable (e.g. "1m 23s", "45s", "2h 3m")
 */
export function formatDuration(ms: number): string {
  const totalSec = Math.floor(ms / 1000)
  const sec = totalSec % 60
  const min = Math.floor(totalSec / 60) % 60
  const hr = Math.floor(totalSec / 3600)

  if (hr > 0) return `${hr}h ${min}m`
  if (min > 0) return `${min}m ${sec}s`
  return `${sec}s`
}

/**
 * Format token counts (e.g. "1.2k", "45.3k", "1.2M")
 */
export function formatTokens(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`
  return `${(n / 1_000_000).toFixed(1)}M`
}

/**
 * Return a short priority label string
 */
export function priorityLabel(p: number): string {
  switch (p) {
    case 1: return 'P1'
    case 2: return 'P2'
    case 3: return 'P3'
    default: return `P${p}`
  }
}
