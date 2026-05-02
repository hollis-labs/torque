import { TaskTable } from '@/components/domain/task-table'
import type { Task } from '@/lib/types'

interface ScopeTaskPanelProps {
  title?: string
  description?: string
  tasks: Task[]
}

export function ScopeTaskPanel({
  title = 'Tasks',
  description = 'Tasks currently grouped under this scope.',
  tasks,
}: ScopeTaskPanelProps) {
  return (
    <section className="rounded-2xl border border-zinc-800/80 bg-zinc-950/70">
      <div className="border-b border-zinc-800/80 px-5 py-4">
        <h2 className="text-sm font-semibold uppercase tracking-[0.18em] text-zinc-400">{title}</h2>
        <p className="mt-1 text-sm text-zinc-500">{description}</p>
      </div>
      <TaskTable tasks={tasks} emptyVariant="no-tasks" />
    </section>
  )
}
