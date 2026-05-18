import { useMemo } from 'react'
import { DonutChart, type DonutSegment } from '@hollis-labs/sysop-ui'
import type { Run } from '@/lib/types'

export interface RunStatusDistributionProps {
  runs: Run[]
  className?: string
  title?: string
  /** Diameter of the donut in px. */
  size?: number
}

type StatusBucket = 'success' | 'error' | 'active' | 'other'

const BUCKET_META: { key: StatusBucket; label: string; color: string }[] = [
  { key: 'success', label: 'success', color: 'var(--color-status-done)' },
  { key: 'error', label: 'error', color: 'var(--color-status-blocked)' },
  { key: 'active', label: 'active', color: 'var(--color-status-doing)' },
  { key: 'other', label: 'other', color: 'var(--color-status-backlog)' },
]

function classify(status: string): StatusBucket {
  const s = (status || '').toLowerCase()
  if (s === 'success' || s === 'done' || s === 'completed') return 'success'
  if (s === 'error' || s === 'failed' || s === 'cancelled' || s === 'canceled' || s === 'timeout')
    return 'error'
  if (s === 'running' || s === 'doing' || s === 'started' || s === 'pending' || s === 'queued')
    return 'active'
  return 'other'
}

/**
 * Run-status donut — a thin wrapper over the kit's DonutChart. Torque
 * classifies each run into a coarse bucket; the kit owns the donut rendering.
 */
export function RunStatusDistribution({
  runs,
  className,
  title = 'Run status',
  size = 120,
}: RunStatusDistributionProps) {
  const segments = useMemo<DonutSegment[]>(() => {
    const counts: Record<StatusBucket, number> = { success: 0, error: 0, active: 0, other: 0 }
    for (const r of runs) counts[classify(r.status)]++
    return BUCKET_META.map((m) => ({ ...m, value: counts[m.key] }))
  }, [runs])

  return (
    <DonutChart
      segments={segments}
      size={size}
      title={title}
      centerLabel="runs"
      className={className}
    />
  )
}
