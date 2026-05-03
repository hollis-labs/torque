import { GripVertical } from 'lucide-react'
import { Link } from 'react-router-dom'
import { StatusBadge } from './status-badge'
import { PriorityBadge } from './priority-badge'
import { CopyableId } from './copyable-id'
import type { Task } from '@/lib/types'

/**
 * Lean read-only row for the Collections page. Intentionally narrower
 * than `TaskRow` from the BoardPage — no sort, no select, no actions
 * menu. Drag handle on the left, copyable ID, title (clickable to
 * detail page), status, priority. Drag-and-drop wiring is owned by the
 * parent table; this component just renders + exposes the drag handle.
 *
 * Refactoring TaskRow to share this footprint is explicitly out of
 * scope for CW-20260503-0005.
 */
interface CollectionTaskRowProps {
  task: Task
  /** Native HTML5 DnD handlers. Owned by the parent so it can wire
   * source/target collection ids and call the right API endpoint. */
  draggable?: boolean
  onDragStart?: (e: React.DragEvent<HTMLTableRowElement>, taskId: string) => void
  onDragOver?: (e: React.DragEvent<HTMLTableRowElement>, taskId: string) => void
  onDragLeave?: (e: React.DragEvent<HTMLTableRowElement>) => void
  onDrop?: (e: React.DragEvent<HTMLTableRowElement>, taskId: string) => void
  onDragEnd?: (e: React.DragEvent<HTMLTableRowElement>) => void
  /** Visual hint for the row currently being hovered as a drop target. */
  dropIndicator?: 'before' | 'after' | null
}

export function CollectionTaskRow({
  task,
  draggable = true,
  onDragStart,
  onDragOver,
  onDragLeave,
  onDrop,
  onDragEnd,
  dropIndicator = null,
}: CollectionTaskRowProps) {
  const indicatorClass =
    dropIndicator === 'before'
      ? 'shadow-[inset_0_2px_0_0_theme(colors.zinc.400)]'
      : dropIndicator === 'after'
      ? 'shadow-[inset_0_-2px_0_0_theme(colors.zinc.400)]'
      : ''

  return (
    <tr
      draggable={draggable}
      onDragStart={(e) => onDragStart?.(e, task.id)}
      onDragOver={(e) => onDragOver?.(e, task.id)}
      onDragLeave={onDragLeave}
      onDrop={(e) => onDrop?.(e, task.id)}
      onDragEnd={onDragEnd}
      className={`bg-zinc-950 hover:bg-zinc-900/35 ${indicatorClass}`}
      data-testid="collection-task-row"
      data-task-id={task.id}
    >
      <td className="w-6 py-2 pl-3 pr-1 text-zinc-600 cursor-grab active:cursor-grabbing">
        <GripVertical className="h-3.5 w-3.5" aria-hidden="true" />
      </td>
      <td className="w-px whitespace-nowrap py-2 pr-2 align-middle">
        <CopyableId id={task.id} />
      </td>
      <td className="py-2 pr-3 align-middle">
        <Link
          to={`/tasks/${task.id}`}
          className="text-[13px] text-zinc-200 hover:text-zinc-100 hover:underline underline-offset-2"
        >
          {task.title}
        </Link>
      </td>
      <td className="w-px whitespace-nowrap py-2 pr-2 align-middle">
        <StatusBadge status={task.status} />
      </td>
      <td className="w-px whitespace-nowrap py-2 pr-3 align-middle">
        <PriorityBadge priority={task.priority} />
      </td>
    </tr>
  )
}
