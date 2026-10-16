import { SEVERITY_STYLE } from './severity-style'
import type { AlertSeverity } from './types'

/** Square count badge. Renders nothing at zero. */
export function AlertBadge({ count, severity = 'info', className = '' }: { count: number; severity?: AlertSeverity; className?: string }) {
  if (count <= 0) return null
  return (
    <span
      data-testid="alert-badge"
      aria-hidden="true"
      className={`inline-flex h-4 min-w-4 items-center justify-center px-1 font-mono text-[9px] font-semibold leading-none ${SEVERITY_STYLE[severity].badge} ${className}`}
    >
      {count > 99 ? '99+' : count}
    </span>
  )
}
