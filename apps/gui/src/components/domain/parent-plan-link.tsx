import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { FolderTree } from 'lucide-react'
import { useApi } from '@/hooks/use-api'
import type { Task } from '@/lib/types'

// ParentPlanLink renders a small "Part of plan: <title>" banner when the
// task has a parent_id AND that parent is a kind=plan task. Silent when
// parent_id is null, missing, or points at a non-plan task — the banner
// opts out rather than surfacing a broken link.
export function ParentPlanLink({ task }: { task: Task }) {
  const api = useApi()
  const parentID = task.parent_id ?? null
  const [parent, setParent] = useState<Task | null>(null)

  useEffect(() => {
    if (!parentID) return
    let cancelled = false
    api
      .getTask(parentID)
      .then((p) => {
        if (!cancelled) setParent(p)
      })
      .catch(() => {
        if (!cancelled) setParent(null)
      })
    return () => {
      cancelled = true
    }
  }, [api, parentID])

  if (!parent || parent.kind !== 'plan') return null

  return (
    <div className="border-b border-zinc-800/80 bg-zinc-950 px-4 py-1.5">
      <Link
        to={`/plans/${parent.id}`}
        className="inline-flex items-center gap-2 text-[11px] text-zinc-400 transition-colors hover:text-zinc-200"
      >
        <FolderTree className="h-3.5 w-3.5" />
        <span>
          Part of plan: <span className="font-medium text-zinc-200">{parent.title}</span>
        </span>
      </Link>
    </div>
  )
}
