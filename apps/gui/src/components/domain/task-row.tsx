import { Link } from 'react-router-dom'
import { MoreHorizontal } from 'lucide-react'
import { StatusBadge } from './status-badge'
import { PriorityBadge } from './priority-badge'
import { formatRelativeTime } from '@/lib/utils'
import type { Task, TaskStatus } from '@/lib/types'

interface TaskRowProps {
  task: Task
  selected?: boolean
  onSelect?: (id: string, selected: boolean) => void
  onTransition?: (id: string, status: TaskStatus) => void
}

export function TaskRow({ task, selected, onSelect }: TaskRowProps) {
  const dateStr = task.updated_at
    ? new Date(task.updated_at).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
    : '-'
  const agoStr = task.updated_at ? formatRelativeTime(task.updated_at) : ''

  return (
    <tr className={`${selected ? 'bg-zinc-900/35' : 'bg-zinc-950 hover:bg-zinc-900/35'}`}>
      {onSelect && (
        <td className="w-8 py-1.5 pl-4 pr-0">
          <input
            type="checkbox"
            checked={selected ?? false}
            onChange={(e) => onSelect(task.id, e.target.checked)}
            className="h-3 w-3 appearance-none rounded-sm border border-zinc-700 bg-zinc-900 checked:bg-zinc-600 checked:border-zinc-500 cursor-pointer"
            aria-label={`Select task ${task.title}`}
          />
        </td>
      )}
      {/* Title + executor subtitle */}
      <td className="px-3 py-1.5 text-left">
        <div className="min-w-0">
          <Link
            to={`/tasks/${task.id}`}
            className="block truncate tracking-[.02em] text-zinc-100 hover:text-zinc-300 transition-colors"
            title={task.title}
          >
            {task.title}
          </Link>
          {task.executor && (
            <span className="text-[10px] text-zinc-600">{task.executor}</span>
          )}
        </div>
      </td>
      {/* Status */}
      <td className="w-px whitespace-nowrap px-1.5 py-1.5">
        <StatusBadge status={task.status} />
      </td>
      {/* Priority */}
      <td className="w-px whitespace-nowrap px-1.5 py-1.5">
        <PriorityBadge priority={task.priority} />
      </td>
      {/* Updated */}
      <td className="w-px whitespace-nowrap pl-1.5 pr-1 py-1.5 text-right">
        <div className="text-[11px] leading-4 text-zinc-400 uppercase tracking-[.12em]">{dateStr}</div>
        <div className="text-[11px] leading-4 text-zinc-600 uppercase tracking-[.12em]">{agoStr}</div>
      </td>
      {/* Actions (inert) */}
      <td className="w-px whitespace-nowrap pl-0 pr-3 py-1.5">
        <button
          type="button"
          className="inline-flex h-6 w-6 items-center justify-center rounded text-zinc-600 hover:bg-zinc-800 hover:text-zinc-400 transition-colors"
          tabIndex={-1}
        >
          <MoreHorizontal className="h-3.5 w-3.5" />
        </button>
      </td>
    </tr>
  )
}
