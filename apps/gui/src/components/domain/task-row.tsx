import { Link, useNavigate } from 'react-router-dom'
import { StatusBadge } from './status-badge'
import { PriorityBadge } from './priority-badge'
import { CopyableId } from './copyable-id'
import { TaskActionsMenu } from './task-actions-menu'
import { CollectionBadge } from './collection-badge'
import { ActiveRunPulse } from './active-run-pulse'
import { TaskStatsCompact } from './task-stats'
import { SubtodosBadge } from './subtodos-panel'
import { formatRelativeTime } from '@/lib/utils'
import { hasBlockedReason, truncateBlockedReason } from '@/lib/blocked-reason'
import { TAG_TEXT_CLASSES } from '@/lib/constants'
import type { Task, TaskStatus } from '@/lib/types'

interface TaskRowProps {
  task: Task
  selected?: boolean
  onSelect?: (id: string, selected: boolean) => void
  onTransition?: (id: string, status: TaskStatus) => void
  onTaskChange?: (task: Task) => void
  onTaskDelete?: (id: string) => void
}

// Any descendant marked `data-row-interactive="true"` owns its own click
// handling and must not trigger row-level navigation. The row click handler
// uses `closest()` to honor the flag for any nested target.
const INTERACTIVE_SELECTOR = '[data-row-interactive="true"]'

export function TaskRow({ task, selected, onSelect, onTaskChange, onTaskDelete }: TaskRowProps) {
  const navigate = useNavigate()
  const dateStr = task.updated_at
    ? new Date(task.updated_at).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
    : '-'
  const agoStr = task.updated_at ? formatRelativeTime(task.updated_at) : ''

  function handleRowClick(e: React.MouseEvent<HTMLTableRowElement>) {
    // Only the primary button should navigate. Let modifier-clicks and
    // middle-clicks fall through so they can't hijack "open in new tab"
    // semantics on the interactive Link children.
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
      return
    }
    const target = e.target as HTMLElement | null
    if (target && target.closest(INTERACTIVE_SELECTOR)) {
      return
    }
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
      data-testid="task-row"
    >
      {onSelect && (
        <td className="w-8 align-top py-1.5 pl-4 pr-0">
          <input
            type="checkbox"
            checked={selected ?? false}
            onChange={(e) => onSelect(task.id, e.target.checked)}
            className="mt-[3px] h-3 w-3 appearance-none rounded-sm border border-zinc-700 bg-zinc-900 checked:bg-zinc-600 checked:border-zinc-500 cursor-pointer"
            aria-label={`Select task ${task.title}`}
            data-row-interactive="true"
            data-testid="task-row-checkbox"
          />
        </td>
      )}
      {/* Title + executor/id/tags subtitle */}
      <td className="w-full max-w-0 px-3 py-1.5 text-left align-top">
        <div className="min-w-0">
          <Link
            to={`/tasks/${task.id}`}
            className="block truncate tracking-[.02em] text-zinc-100 hover:text-zinc-300 transition-colors"
            title={task.title}
            data-row-interactive="true"
          >
            {task.title}
          </Link>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
            {task.executor && (
              <span className="text-[10px] text-zinc-600">{task.executor}</span>
            )}
            <span className="text-[10px] font-mono text-zinc-600">id:</span>
            <CopyableId id={task.id} />
            <SubtodosBadge subtodos={task.subtodos} />
            <CollectionBadge task={task} onChange={onTaskChange} />
            {task.tags && task.tags.length > 0 && (
              <>
                <span className="text-[10px] font-mono text-zinc-600">tags:</span>
                <span className="text-[10px] font-mono text-zinc-600">
                  {task.tags.slice(0, 3).map((tag, idx) => (
                    <span key={tag.slug}>
                      <span
                        className={TAG_TEXT_CLASSES[tag.color]}
                        title={tag.description || tag.name}
                      >
                        {tag.name}
                      </span>
                      {idx < Math.min(task.tags!.length, 3) - 1 && (
                        <span className="text-zinc-700">, </span>
                      )}
                    </span>
                  ))}
                  {task.tags.length > 3 && (
                    <span className="text-zinc-600"> +{task.tags.length - 3}</span>
                  )}
                </span>
              </>
            )}
          </div>
        </div>
      </td>
      {/* Status */}
      <td className="w-px whitespace-nowrap px-1.5 py-1.5">
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
      {/* Cost / tokens roll-up */}
      <td className="w-px whitespace-nowrap px-1.5 py-1.5">
        <TaskStatsCompact stats={task.stats} />
      </td>
      {/* Updated */}
      <td className="w-px whitespace-nowrap px-1.5 py-1.5">
        <div className="text-[11px] leading-4 text-zinc-400 uppercase tracking-[.12em]">{dateStr}</div>
        <div className="text-[11px] leading-4 text-zinc-600 uppercase tracking-[.12em]">{agoStr}</div>
      </td>
      {/* Actions */}
      <td className="w-px whitespace-nowrap pl-1 pr-3 py-1.5">
        <TaskActionsMenu
          task={task}
          onChange={onTaskChange}
          onDelete={onTaskDelete}
        />
      </td>
    </tr>
  )
}
