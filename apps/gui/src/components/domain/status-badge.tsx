import { STATUS_COLORS, DEFAULT_STATUS_COLOR } from '@/lib/constants'

interface StatusBadgeProps {
  status: string
  className?: string
}

export function StatusBadge({ status, className = '' }: StatusBadgeProps) {
  const normalized = (status || '').toLowerCase()
  const colors = STATUS_COLORS[normalized] ?? DEFAULT_STATUS_COLOR

  return (
    <span
      className={`inline-flex items-center gap-2 rounded border px-2 py-1 text-[11px] uppercase tracking-[0.14em] ${colors.border} ${colors.bg} ${colors.text} ${className}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${colors.dot}`} />
      <span>{status || 'unknown'}</span>
    </span>
  )
}
