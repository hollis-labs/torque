import { Tooltip, TooltipContent, TooltipTrigger, statusTone, type StatusTone } from '@hollis-labs/sysop-ui'
import { CONTAINER_STATUS_COLORS } from '@/lib/constants'

interface StatusBadgeProps {
  status: string
  className?: string
  /**
   * Optional tooltip text shown on hover/focus. Used to surface the
   * blocked_reason on a status pill without making the user click through.
   */
  tooltip?: string
}

/**
 * Torque status pill — a thin domain wrapper over the kit's `statusTone`.
 * Adds two Torque-specific concerns the generic kit `StatusBadge` doesn't
 * carry: container statuses (`active`/`inactive`/`completed`) and an optional
 * hover tooltip for surfacing a blocked reason.
 */
export function StatusBadge({ status, className = '', tooltip }: StatusBadgeProps) {
  const normalized = (status || '').toLowerCase()
  const container = CONTAINER_STATUS_COLORS[normalized]
  const tone: StatusTone = container ?? statusTone(normalized)

  const badge = (
    <span
      className={`inline-flex items-center gap-2 rounded border px-2 py-1 text-[11px] uppercase tracking-[0.14em] ${tone.border} ${tone.bg} ${tone.text} ${className}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${tone.dot}`} />
      <span>{status || 'unknown'}</span>
    </span>
  )

  if (!tooltip) return badge

  return (
    <Tooltip>
      <TooltipTrigger
        render={(props) => (
          <span {...props} className="inline-flex" tabIndex={0}>
            {badge}
          </span>
        )}
      />
      <TooltipContent className="max-w-sm whitespace-pre-wrap text-left">
        {tooltip}
      </TooltipContent>
    </Tooltip>
  )
}
