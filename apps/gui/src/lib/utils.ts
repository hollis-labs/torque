import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

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
 * Format a cost value in dollars (e.g. "$0.0042")
 */
export function formatCost(cost: number): string {
  if (cost === 0) return '$0.00'
  if (cost < 0.01) return `$${cost.toFixed(4)}`
  return `$${cost.toFixed(2)}`
}

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

/**
 * Parse a comma-separated tags string into an array of trimmed, non-empty strings
 */
export function parseTags(tags: string): string[] {
  if (!tags || tags.trim() === '') return []
  return tags.split(',').map(t => t.trim()).filter(Boolean)
}
