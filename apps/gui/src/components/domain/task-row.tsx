import { Link, useNavigate } from 'react-router-dom'
import { StatusBadge } from './status-badge'
import { PriorityBadge } from './priority-badge'
import { TagChip } from './tag-chip'
import { CopyableId } from './copyable-id'
import { TaskActionsMenu } from './task-actions-menu'
import { ActiveRunPulse } from './active-run-pulse'
import { formatRelativeTime } from '@/lib/utils'
import { hasBlockedReason, truncateBlockedReason } from '@/lib/blocked-reason'
import type { Task, TaskStatus } from '@/lib/types'

interface TaskRowProps {
  task: Task
  selected?: boolean
  onSelect?: (id: string, selected: boolean) => void
  onTransition?: (id: string, status: TaskStatus) => void
  onTaskChange?: (task: Task) => void
  onTaskDelete?: (id: string) => void
}

function stop(e: React.SyntheticEvent) {
  e.stopPropagation()
}

export function TaskRow({ task, selected, onSelect, onTaskChange, onTaskDelete }: TaskRowProps) {
  const navigate = useNavigate()
  const dateStr = task.updated_at
    ? new Date(task.updated_at).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
    : '-'
  const agoStr = task.updated_at ? formatRelativeTime(task.updated_at) : ''

  function handleRowClick() {
    navigate(`/tasks/${task.id}`)
  }

  function handleRowKeyDown(e: React.KeyboardEvent<HTMLTableRowElement>) {
    if (e.target !== e.currentTarget) return
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      navigate(`/tasks/${task.id}`)
    }
  }

  return (
    <tr
      className={`cursor-pointer outline-none focus-visible:ring-1 focus-visible:ring-zinc-600 [&>td]:cursor-pointer ${selected ? 'bg-zinc-900/35' : 'bg-zinc-950 hover:bg-zinc-900/35'}`}
      onClick={handleRowClick}
      onKeyDown={handleRowKeyDown}
      tabIndex={0}
      role="link"
      aria-label={`Open task ${task.title}`}
    >
      {onSelect && (
        <td className="w-8 py-1.5 pl-4 pr-0" onClick={stop}>
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
            onClick={stop}
            className="block truncate tracking-[.02em] text-zinc-100 hover:text-zinc-300 transition-colors"
            title={task.title}
          >
            {task.title}
          </Link>
          <div className="flex items-center gap-2">
            {task.executor && (
              <span className="text-[10px] text-zinc-600">{task.executor}</span>
            )}
            <CopyableId id={task.id} />
          </div>
          {task.tags && task.tags.length > 0 && (
            <div className="mt-0.5 flex items-center gap-1">
              {task.tags.slice(0, 3).map((tag) => (
                <TagChip key={tag.slug} tag={tag} />
              ))}
              {task.tags.length > 3 && (
                <span className="text-[10px] text-zinc-500">
                  +{task.tags.length - 3} more
                </span>
              )}
            </div>
          )}
        </div>
      </td>
      {/* Status */}
      <td className="w-px whitespace-nowrap px-1.5 py-1.5" onClick={stop}>
        <div className="flex items-center gap-2">
          <StatusBadge
            status={task.status}
            tooltip={
              hasBlockedReason(task)
                ? truncateBlockedReason(task.blocked_reason)
                : undefined
            }
          />
          <ActiveRunPulse taskId={task.id} />
        </div>
      </td>
      {/* Priority */}
      <td className="w-px whitespace-nowrap px-1.5 py-1.5">
        <PriorityBadge priority={task.priority} />
      </td>
      {/* Updated */}
      <td className="w-px whitespace-nowrap px-1.5 py-1.5">
        <div className="text-[11px] leading-4 text-zinc-400 uppercase tracking-[.12em]">{dateStr}</div>
        <div className="text-[11px] leading-4 text-zinc-600 uppercase tracking-[.12em]">{agoStr}</div>
      </td>
      {/* Actions */}
      <td className="w-px whitespace-nowrap pl-1 pr-3 py-1.5" onClick={stop}>
        <TaskActionsMenu
          task={task}
          onChange={onTaskChange}
          onDelete={onTaskDelete}
        />
      </td>
    </tr>
  )
}
