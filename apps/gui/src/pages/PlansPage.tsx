import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Plus } from 'lucide-react'
import { PageHeader, EmptyState, Skeleton, Button, CopyableId } from '@hollis-labs/sysop-ui'
import { CreatePlanDialog } from '@/components/domain/create-plan-dialog'
import { useApi } from '@/hooks/use-api'
import { usePagedList } from '@/hooks/use-paged-list'
import { ListPageControls } from '@/components/domain/list-page-controls'
import type { PlanDetail, Task } from '@/lib/types'

interface PlanRow {
  task: Task
  phaseCount: number
  totalChildren: number
  done: number
}

function PlansSkeleton() {
  return (
    <div className="divide-y divide-zinc-800/80 border-y border-zinc-800/80">
      {Array.from({ length: 6 }).map((_, i) => (
        <Skeleton key={i} className="h-20 w-full rounded-none" />
      ))}
    </div>
  )
}

export default function PlansPage() {
  const api = useApi()
  const navigate = useNavigate()
  const [dialogOpen, setDialogOpen] = useState(false)
  const page = usePagedList({
    params: { sort_by: 'updated_at', sort_dir: 'desc' as const, include_total: true },
    fetchPage: async ({ params, cursor, signal }) => {
      const result = await api.listPlans({ ...params, cursor }, signal)
      const items = await Promise.all(result.items.map(async task => {
        try {
          const detail = await api.getPlan(task.id)
          return { task: detail.task, phaseCount: detail.plan.phases.length, totalChildren: detail.progress.total_children, done: detail.progress.done } satisfies PlanRow
        } catch { return { task, phaseCount: 0, totalChildren: 0, done: 0 } satisfies PlanRow }
      }))
      return { items, meta: result.meta }
    },
    getId: row => row.task.id,
  })
  const { items: rows, loading, error, reload: fetchPlans } = page

  function handleCreated(plan: PlanDetail) {
    navigate(`/plans/${plan.task.id}`)
  }

  return (
    <div className="flex h-full min-w-0 flex-col">
      <PageHeader title="Plans">
        <Button size="sm" onClick={() => setDialogOpen(true)} className="gap-1">
          <Plus className="h-3.5 w-3.5" />
          New plan
        </Button>
      </PageHeader>

      <ListPageControls page={page} label="plans" />
      <div className="flex-1 overflow-auto">
        {loading && rows.length === 0 ? (
          <PlansSkeleton />
        ) : error && rows.length === 0 ? (
          <EmptyState variant="error" title="Something went wrong" description={error.message} action={{ label: 'Retry', onClick: fetchPlans }} />
        ) : rows.length === 0 ? (
          <EmptyState
            variant="empty"
            title="No plans yet"
            description="Group tasks into phases and track their progress."
            action={{ label: 'New plan', onClick: () => setDialogOpen(true) }}
          />
        ) : (
          <div className="divide-y divide-zinc-800/80 border-y border-zinc-800/80">
            {rows.map((row) => (
              <PlanCard key={row.task.id} row={row} onClick={() => navigate(`/plans/${row.task.id}`)} />
            ))}
          </div>
        )}
      </div>

      <CreatePlanDialog open={dialogOpen} onOpenChange={setDialogOpen} onCreated={handleCreated} />
    </div>
  )
}

function PlanCard({ row, onClick }: { row: PlanRow; onClick: () => void }) {
  const pct = row.totalChildren === 0 ? 0 : Math.round((row.done / row.totalChildren) * 100)
  return (
    <div
      className="group flex w-full min-w-0 flex-col gap-3 px-4 py-3 text-left transition-colors hover:bg-zinc-900/35 sm:flex-row sm:items-center sm:justify-between"
    >
      <div className="flex min-w-0 flex-1 items-start justify-between gap-3">
        <div className="min-w-0">
          <button type="button" onClick={onClick} className="text-left [overflow-wrap:anywhere] text-[13px] font-medium text-zinc-100 hover:underline">{row.task.title}</button>
          <div className="mt-0.5">
            <CopyableId id={row.task.id} />
          </div>
        </div>
        <span className="shrink-0 rounded border border-zinc-800 bg-zinc-950 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-zinc-400">
          {row.phaseCount} {row.phaseCount === 1 ? 'phase' : 'phases'}
        </span>
      </div>

      <div className="flex w-full shrink-0 items-center gap-2 text-xs sm:ml-6 sm:w-52 text-zinc-400">
        <span>
          {row.done}/{row.totalChildren} done
        </span>
        <div className="h-1 flex-1 overflow-hidden rounded-full bg-zinc-800">
          <div
            className="h-full bg-emerald-500 transition-all"
            style={{ width: `${pct}%` }}
            aria-label={`${pct}% complete`}
          />
        </div>
        <span className="w-8 text-right font-mono text-[10px] text-zinc-500">{pct}%</span>
      </div>
    </div>
  )
}
