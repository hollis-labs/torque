import { Link } from 'react-router-dom'
import { PlannerStatus } from '@/components/planner/planner-status'
import type { PlannerTaskBrief } from '@/lib/types'

/**
 * A reference to a Torque task: its id, and once known its live title and
 * status. `brief` is undefined while loading and null when Torque has no such task.
 */
export function TorqueTaskRef({ id, brief, label }: { id: string; brief: PlannerTaskBrief | null | undefined; label?: string }) {
  return (
    <span className="inline-flex min-w-0 flex-wrap items-center gap-2">
      <Link to={`/tasks/${encodeURIComponent(id)}`} className="font-mono text-xs text-primary hover:underline">
        {id}
      </Link>
      {label && <span className="text-sm text-muted-foreground">{label}</span>}
      {brief && <span className="min-w-0 truncate text-sm">{brief.title}</span>}
      {brief && <PlannerStatus status={brief.status} className="!px-1.5 !py-0.5 !text-[10px]" />}
      {brief === null && <span className="text-xs text-muted-foreground">not found in Torque</span>}
    </span>
  )
}
