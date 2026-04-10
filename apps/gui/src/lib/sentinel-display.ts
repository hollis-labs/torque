/**
 * Shape returned by every sentinel formatter. The `mode` field lets callers
 * apply different Tailwind classes depending on whether the value is a real
 * number, "unlimited", or "default" (fallback).
 */
export interface SentinelDisplay {
  label: string
  mode: 'default' | 'unlimited' | 'value'
}

const DEFAULT: SentinelDisplay = { label: 'default', mode: 'default' }
const UNLIMITED: SentinelDisplay = { label: 'unlimited', mode: 'unlimited' }

export function formatCostBudget(value: number | null): SentinelDisplay {
  if (value === null) return DEFAULT
  if (value === -1) return UNLIMITED
  return { label: `$${value.toFixed(2)}`, mode: 'value' }
}

export function formatDurationMs(value: number | null): SentinelDisplay {
  if (value === null) return DEFAULT
  if (value === -1) return UNLIMITED
  return { label: formatDuration(value), mode: 'value' }
}

export function formatTokenBudget(value: number | null): SentinelDisplay {
  if (value === null) return DEFAULT
  if (value === -1) return UNLIMITED
  return { label: value.toLocaleString('en-US'), mode: 'value' }
}

function formatDuration(ms: number): string {
  if (ms === 0) return '0s'
  const hours = Math.floor(ms / 3_600_000)
  const minutes = Math.floor((ms % 3_600_000) / 60_000)
  const seconds = Math.floor((ms % 60_000) / 1000)
  const parts: string[] = []
  if (hours > 0) parts.push(`${hours}h`)
  if (minutes > 0) parts.push(`${minutes}m`)
  if (seconds > 0) parts.push(`${seconds}s`)
  return parts.join(' ') || '0s'
}
