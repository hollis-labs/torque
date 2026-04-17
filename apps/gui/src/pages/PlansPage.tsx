import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Plus } from 'lucide-react'
import { PageHeader } from '@/components/domain/page-header'
import { EmptyState } from '@/components/domain/empty-state'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { CopyableId } from '@/components/domain/copyable-id'
import { CreatePlanDialog } from '@/components/domain/create-plan-dialog'
import { useApi } from '@/hooks/use-api'
import type { PlanDetail, Task } from '@/lib/types'

interface PlanRow {
  task: Task
  phaseCount: number
  totalChildren: number
  done: number
}

function PlansSkeleton() {
  return (
    <div className="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3">
      {Array.from({ length: 6 }).map((_, i) => (
        <Skeleton key={i} className="h-28 w-full rounded-md" />
      ))}
    </div>
  )
}

export default function PlansPage() {
  const api = useApi()
  const navigate = useNavigate()
  const [rows, setRows] = useState<PlanRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)

  const fetchPlans = useCallback(async () => {
    try {
      const { plans } = await api.listPlans()
      // Hydrate each row with phase count + progress. Small N — the listing
      // view never shows enough plans to make this expensive.
      const details = await Promise.all(
        plans.map(async (task) => {
          try {
            const detail = await api.getPlan(task.id)
            return {
              task: detail.task,
              phaseCount: detail.plan.phases.length,
              totalChildren: detail.progress.total_children,
              done: detail.progress.done,
            } satisfies PlanRow
          } catch {
            return { task, phaseCount: 0, totalChildren: 0, done: 0 } satisfies PlanRow
          }
        })
      )
      setRows(details)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load plans')
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    setLoading(true)
    fetchPlans()
  }, [fetchPlans])

  function handleCreated(plan: PlanDetail) {
    navigate(`/plans/${plan.task.id}`)
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Plans">
        <Button size="sm" onClick={() => setDialogOpen(true)} className="gap-1">
          <Plus className="h-3.5 w-3.5" />
          New plan
        </Button>
      </PageHeader>

      <div className="flex-1 overflow-auto">
        {loading ? (
          <PlansSkeleton />
        ) : error ? (
          <EmptyState variant="error" description={error} action={{ label: 'Retry', onClick: fetchPlans }} />
        ) : rows.length === 0 ? (
          <EmptyState
            variant="no-tasks"
            title="No plans yet"
            description="Plans group phase-scoped execution children. Click New plan to create one."
            action={{ label: 'New plan', onClick: () => setDialogOpen(true) }}
          />
        ) : (
          <div className="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3">
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
    <button
      type="button"
      onClick={onClick}
      className="group flex flex-col gap-3 rounded-md border border-zinc-800 bg-zinc-900/40 p-3 text-left transition-colors hover:border-zinc-700 hover:bg-zinc-900"
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold text-zinc-100">{row.task.title}</div>
          <div className="mt-0.5">
            <CopyableId id={row.task.id} />
          </div>
        </div>
        <span className="shrink-0 rounded border border-zinc-800 bg-zinc-950 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-zinc-400">
          {row.phaseCount} {row.phaseCount === 1 ? 'phase' : 'phases'}
        </span>
      </div>

      <div className="flex items-center gap-2 text-[11px] text-zinc-400">
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
    </button>
  )
}
