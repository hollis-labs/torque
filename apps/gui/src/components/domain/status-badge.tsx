import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { STATUS_COLORS, CONTAINER_STATUS_COLORS, DEFAULT_STATUS_COLOR } from '@/lib/constants'

interface StatusBadgeProps {
  status: string
  className?: string
  /**
   * Optional tooltip text shown on hover/focus. Used to surface the
   * blocked_reason on a status pill without making the user click through.
   */
  tooltip?: string
}

export function StatusBadge({ status, className = '', tooltip }: StatusBadgeProps) {
  const normalized = (status || '').toLowerCase()
  const colors = STATUS_COLORS[normalized] ?? CONTAINER_STATUS_COLORS[normalized] ?? DEFAULT_STATUS_COLOR

  const badge = (
    <span
      className={`inline-flex items-center gap-2 rounded border px-2 py-1 text-[11px] uppercase tracking-[0.14em] ${colors.border} ${colors.bg} ${colors.text} ${className}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${colors.dot}`} />
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
