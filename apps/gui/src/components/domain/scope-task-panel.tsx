import { Button } from '@hollis-labs/sysop-ui'
import { TaskTable } from '@/components/domain/task-table'
import type { TaskSummary } from '@/lib/types'

interface ScopeTaskPanelProps {
  title?: string
  description?: string
  tasks: TaskSummary[]
  /** Size of the whole scope when `tasks` is one page of it. */
  total?: number
  onLoadMore?: () => void
  loadingMore?: boolean
}

export function ScopeTaskPanel({
  title = 'Tasks',
  description = 'Tasks currently grouped under this scope.',
  tasks,
  total,
  onLoadMore,
  loadingMore = false,
}: ScopeTaskPanelProps) {
  const paged = total !== undefined && total > tasks.length
  return (
    <section className="rounded-2xl border border-zinc-800/80 bg-zinc-950/70">
      <div className="border-b border-zinc-800/80 px-5 py-4">
        <h2 className="text-sm font-semibold uppercase tracking-[0.18em] text-zinc-400">{title}</h2>
        <p className="mt-1 text-sm text-zinc-500">{description}</p>
      </div>
      <TaskTable tasks={tasks} emptyVariant="no-tasks" />
      {paged && (
        <div className="flex items-center justify-between gap-4 border-t border-zinc-800/80 px-5 py-3 text-sm text-zinc-500">
          <span>
            Showing {tasks.length} of {total}, most recently updated first
          </span>
          {onLoadMore && (
            <Button variant="outline" size="sm" onClick={onLoadMore} disabled={loadingMore}>
              {loadingMore ? 'Loading…' : 'Load more'}
            </Button>
          )}
        </div>
      )}
    </section>
  )
}
