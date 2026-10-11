import { statusTone } from '@hollis-labs/sysop-ui'
import { statusToneKey } from '@/lib/planner'

/** A planner status pill. The planner has its own vocabulary; it borrows the kit's colors. */
export function PlannerStatus({ status, className = '' }: { status?: string; className?: string }) {
  if (!status) return null
  const tone = statusTone(statusToneKey(status))
  return (
    <span
      className={`inline-flex items-center gap-2 rounded border px-2 py-1 text-[11px] uppercase tracking-[0.14em] ${tone.border} ${tone.bg} ${tone.text} ${className}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${tone.dot}`} />
      <span>{status}</span>
    </span>
  )
}
