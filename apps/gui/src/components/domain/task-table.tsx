import { useEffect, useMemo, useRef, useState, type RefObject } from 'react'
import { EmptyState } from '@hollis-labs/sysop-ui'
import { TaskRow } from './task-row'
import type { Task, TaskStatus } from '@/lib/types'

const EMPTY_COPY = {
  'no-tasks': {
    variant: 'empty' as const,
    title: 'No tasks yet',
    description: 'Create your first task to get started.',
  },
  'no-results': {
    variant: 'no-results' as const,
    title: 'No results found',
    description: 'Try adjusting your filters or search query.',
  },
}

type SortKey = 'status' | 'priority' | 'title' | 'updated_at'
type SortDir = 'asc' | 'desc'

const PAGE_SIZE = 50

interface TaskTableProps {
  tasks: Task[]
  loading?: boolean
  onTransition?: (id: string, status: TaskStatus) => void
  onTaskChange?: (task: Task) => void
  onTaskDelete?: (id: string) => void
  emptyVariant?: 'no-tasks' | 'no-results'
  /** Fires with the current render order (post-sort) whenever it changes. */
  onVisibleOrderChange?: (orderedIds: string[]) => void
  /** Scroll container used as the IntersectionObserver root for infinite scroll. */
  scrollRootRef?: RefObject<HTMLElement | null>
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
  { key: 'title',      label: 'Task'    },
  { key: 'status',     label: 'Status'  },
  { key: 'priority',   label: 'Pri'     },
  { key: 'updated_at', label: 'Date' },
]

export function TaskTable({
  tasks,
  onTransition,
  onTaskChange,
  onTaskDelete,
  emptyVariant = 'no-tasks',
  onVisibleOrderChange,
  scrollRootRef,
}: TaskTableProps) {
  const [sortKey, setSortKey] = useState<SortKey>('updated_at')
  const [sortDir, setSortDir] = useState<SortDir>('desc')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE)
  const sentinelRef = useRef<HTMLTableRowElement | null>(null)

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
      setSelected(new Set(visible.map((t) => t.id)))
    } else {
      setSelected(new Set())
    }
  }

  const sorted = useMemo(() => sortTasks(tasks, sortKey, sortDir), [tasks, sortKey, sortDir])

  // Reset the window to the first page whenever the underlying list or sort
  // changes, so filter/sort changes always show the top of the new list.
  useEffect(() => {
    setVisibleCount(PAGE_SIZE)
  }, [tasks, sortKey, sortDir])

  const visible = useMemo(() => sorted.slice(0, visibleCount), [sorted, visibleCount])
  const hasMore = visibleCount < sorted.length

  // Publish the currently-rendered order so callers (e.g. BoardPage) can
  // persist a cursor that matches what the user is actually seeing, not
  // just the raw fetch order.
  useEffect(() => {
    if (!onVisibleOrderChange) return
    onVisibleOrderChange(visible.map((t) => t.id))
  }, [visible, onVisibleOrderChange])

  // Infinite scroll: watch a sentinel at the end of the rendered rows and
  // bump the window by PAGE_SIZE when it intersects the scroll root.
  useEffect(() => {
    if (!hasMore) return
    const el = sentinelRef.current
    if (!el) return
    const root = scrollRootRef?.current ?? null
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisibleCount((c) => Math.min(c + PAGE_SIZE, sorted.length))
        }
      },
      { root, rootMargin: '200px 0px' }
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [hasMore, sorted.length, scrollRootRef])

  if (tasks.length === 0) {
    const copy = EMPTY_COPY[emptyVariant]
    return <EmptyState variant={copy.variant} title={copy.title} description={copy.description} />
  }

  const allSelected = visible.length > 0 && visible.every((t) => selected.has(t.id))

  return (
    <div className="overflow-x-auto">
      <table className="min-w-full">
        <thead className="text-[10px] uppercase tracking-[.28em] text-zinc-500">
          <tr className="border-b border-zinc-800/80">
            <th className="w-8 py-1.5 pl-[14px] pr-0">
              <input
                type="checkbox"
                checked={allSelected}
                onChange={handleSelectAll}
                className="h-3 w-3 appearance-none rounded-sm border border-zinc-700 bg-zinc-900 checked:bg-zinc-600 checked:border-zinc-500 cursor-pointer"
                aria-label="Select all tasks"
              />
            </th>
            {COLUMNS.flatMap(({ key, label }) => {
              const isSorted = sortKey === key
              const isTitle = key === 'title'
              const header = (
                <th
                  key={key}
                  className={`py-1.5 font-medium ${isTitle ? 'px-3 text-left' : 'w-px whitespace-nowrap px-1.5'}`}
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
              if (key === 'priority') {
                return [
                  header,
                  <th
                    key="usage"
                    className="w-px whitespace-nowrap px-1.5 py-1.5 font-medium text-zinc-500"
                  >
                    Usage
                  </th>,
                ]
              }
              return [header]
            })}
            {/* Actions header spacer */}
            <th className="w-px pr-3" />
          </tr>
        </thead>
        <tbody className="divide-y divide-zinc-800/60 text-[13px] leading-4">
          {visible.map((task) => (
            <TaskRow
              key={task.id}
              task={task}
              selected={selected.has(task.id)}
              onSelect={handleSelect}
              onTransition={onTransition}
              onTaskChange={onTaskChange}
              onTaskDelete={onTaskDelete}
            />
          ))}
          {hasMore && (
            <tr ref={sentinelRef} aria-hidden="true">
              <td colSpan={COLUMNS.length + 3} className="py-3 text-center text-[11px] text-zinc-600">
                Loading more… ({visible.length} of {sorted.length})
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  )
}
