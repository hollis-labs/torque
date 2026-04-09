import { Link } from 'react-router-dom'
import { StatusBadge } from './status-badge'
import { PriorityBadge } from './priority-badge'
import { formatRelativeTime, parseTags } from '@/lib/utils'
import type { Task, TaskStatus } from '@/lib/types'

interface TaskRowProps {
  task: Task
  selected?: boolean
  onSelect?: (id: string, selected: boolean) => void
  onTransition?: (id: string, status: TaskStatus) => void
}

export function TaskRow({ task, selected, onSelect }: TaskRowProps) {
  const tags = parseTags(task.tags)
  const dateStr = task.updated_at
    ? new Date(task.updated_at).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
    : '-'
  const agoStr = task.updated_at ? formatRelativeTime(task.updated_at) : ''

  return (
    <tr className={`${selected ? 'bg-zinc-900/35' : 'bg-zinc-950 hover:bg-zinc-900/35'}`}>
      {onSelect && (
        <td className="w-10 py-1.5 pr-0 pl-3">
          <input
            type="checkbox"
            checked={selected ?? false}
            onChange={(e) => onSelect(task.id, e.target.checked)}
            className="h-3.5 w-3.5 rounded border-zinc-700 bg-zinc-900 accent-zinc-400 cursor-pointer"
            aria-label={`Select task ${task.title}`}
          />
        </td>
      )}
      {/* Status */}
      <td className="w-px whitespace-nowrap px-2 py-1.5">
        <StatusBadge status={task.status} />
      </td>
      {/* Priority */}
      <td className="w-px whitespace-nowrap px-2 py-1.5">
        <PriorityBadge priority={task.priority} />
      </td>
      {/* Title + tags */}
      <td className="px-3 py-1.5 text-left">
        <div className="min-w-0">
          <Link
            to={`/tasks/${task.id}`}
            className="block truncate tracking-[.02em] text-zinc-100 hover:text-zinc-300 transition-colors"
            title={task.title}
          >
            {task.title}
          </Link>
          {tags.length > 0 && (
            <div className="mt-0.5 flex flex-wrap gap-1">
              {tags.map((tag) => (
                <span
                  key={tag}
                  className="rounded border border-zinc-800/80 bg-zinc-900 px-1 py-0.5 font-mono text-[10px] text-zinc-500"
                >
                  {tag}
                </span>
              ))}
            </div>
          )}
        </div>
      </td>
      {/* Executor */}
      <td className="w-px whitespace-nowrap px-2 py-1.5 text-right text-[12px] text-zinc-500">
        {task.executor || <span className="italic text-zinc-600">—</span>}
      </td>
      {/* Updated */}
      <td className="w-px whitespace-nowrap px-2 py-1.5 text-right">
        <div className="text-[11px] leading-4 text-zinc-400 uppercase tracking-[.12em]">{dateStr}</div>
        <div className="text-[11px] leading-4 text-zinc-600 uppercase tracking-[.12em]">{agoStr}</div>
      </td>
    </tr>
  )
}
