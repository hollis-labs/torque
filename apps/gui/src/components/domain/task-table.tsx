import { useState } from 'react'
import { TaskRow } from './task-row'
import { EmptyState } from './empty-state'
import type { Task, TaskStatus } from '@/lib/types'

type SortKey = 'status' | 'priority' | 'title' | 'executor' | 'updated_at'
type SortDir = 'asc' | 'desc'

interface TaskTableProps {
  tasks: Task[]
  loading?: boolean
  onTransition?: (id: string, status: TaskStatus) => void
  emptyVariant?: 'no-tasks' | 'no-results'
}

function sortTasks(tasks: Task[], key: SortKey, dir: SortDir): Task[] {
  return [...tasks].sort((a, b) => {
    let av: string | number = a[key] ?? ''
    let bv: string | number = b[key] ?? ''
    if (key === 'priority') { av = a.priority; bv = b.priority }
    const cmp = av < bv ? -1 : av > bv ? 1 : 0
    return dir === 'asc' ? cmp : -cmp
  })
}

const COLUMNS: { key: SortKey; label: string }[] = [
  { key: 'status',     label: 'Status'   },
  { key: 'priority',   label: 'Pri'      },
  { key: 'title',      label: 'Task'     },
  { key: 'executor',   label: 'Executor' },
  { key: 'updated_at', label: 'Updated'  },
]

export function TaskTable({ tasks, onTransition, emptyVariant = 'no-tasks' }: TaskTableProps) {
  const [sortKey, setSortKey] = useState<SortKey>('updated_at')
  const [sortDir, setSortDir] = useState<SortDir>('desc')
  const [selected, setSelected] = useState<Set<string>>(new Set())

  function handleSortClick(key: SortKey) {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  function handleSelect(id: string, isSelected: boolean) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (isSelected) next.add(id)
      else next.delete(id)
      return next
    })
  }

  function handleSelectAll(e: React.ChangeEvent<HTMLInputElement>) {
    if (e.target.checked) {
      setSelected(new Set(sorted.map((t) => t.id)))
    } else {
      setSelected(new Set())
    }
  }

  const sorted = sortTasks(tasks, sortKey, sortDir)

  if (tasks.length === 0) {
    return <EmptyState variant={emptyVariant} />
  }

  const allSelected = sorted.length > 0 && sorted.every((t) => selected.has(t.id))

  return (
    <div className="overflow-x-auto">
      <table className="min-w-full">
        <thead className="text-[10px] uppercase tracking-[.28em] text-zinc-500">
          <tr className="border-b border-zinc-800/80">
            <th className="w-10 py-2 pr-0 pl-3">
              <input
                type="checkbox"
                checked={allSelected}
                onChange={handleSelectAll}
                className="h-3.5 w-3.5 rounded border-zinc-700 bg-zinc-900 accent-zinc-400 cursor-pointer"
                aria-label="Select all tasks"
              />
            </th>
            {COLUMNS.map(({ key, label }) => {
              const isSorted = sortKey === key
              const isTitle = key === 'title'
              return (
                <th
                  key={key}
                  className={`py-2 font-medium ${isTitle ? 'px-3 text-left' : 'w-px whitespace-nowrap px-2 text-right'}`}
                >
                  <button
                    type="button"
                    className="inline-flex items-center gap-1 hover:text-zinc-300 transition-colors"
                    onClick={() => handleSortClick(key)}
                  >
                    {label}
                    <span className={isSorted ? 'text-zinc-200' : 'text-zinc-700'}>
                      {isSorted ? (sortDir === 'asc' ? '↑' : '↓') : '⇕'}
                    </span>
                  </button>
                </th>
              )
            })}
          </tr>
        </thead>
        <tbody className="divide-y divide-zinc-800/60 text-[13px] leading-4">
          {sorted.map((task) => (
            <TaskRow
              key={task.id}
              task={task}
              selected={selected.has(task.id)}
              onSelect={handleSelect}
              onTransition={onTransition}
            />
          ))}
        </tbody>
      </table>
    </div>
  )
}
