import { forwardRef, type CSSProperties, type HTMLAttributes } from 'react'
import { GripVertical } from 'lucide-react'
import { Link } from 'react-router-dom'
import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { DraggableSyntheticListeners } from '@dnd-kit/core'
import { StatusBadge } from './status-badge'
import { PriorityBadge, CopyableId } from '@hollis-labs/sysop-ui'
import type { Task } from '@/lib/types'

/**
 * Lean read-only row for the Collections page. Intentionally narrower
 * than `TaskRow` from the BoardPage — no sort, no select, no actions
 * menu. Drag handle on the left, copyable ID, title (clickable to
 * detail page), status, priority.
 *
 * Drag-and-drop is wired via `@dnd-kit/sortable`. The row participates
 * in a `SortableContext` keyed by `task.id`; the parent page owns the
 * `DndContext` and translates drag events into API calls. The drag
 * handle (GripVertical icon cell) gets the listeners — pointer-down
 * anywhere else just selects text or follows the title link.
 */
interface CollectionTaskRowProps {
  task: Task
  /** Render as the drag overlay clone — no useSortable wiring, just
   * static visuals. The parent's `<DragOverlay>` passes this. */
  asOverlay?: boolean
}

/**
 * Inner static row used both for the live row (with refs/listeners
 * applied) and for the DragOverlay clone (no listeners, no transform).
 * Splitting this out keeps the dnd-kit boilerplate co-located in the
 * outer wrapper while leaving the table-cell layout reusable.
 */
const StaticRowMarkup = forwardRef<
  HTMLTableRowElement,
  HTMLAttributes<HTMLTableRowElement> & {
    task: Task
    listeners?: DraggableSyntheticListeners
    isDragging?: boolean
    isOverlay?: boolean
  }
>(function StaticRowMarkup({ task, listeners, isDragging, isOverlay, className, ...rest }, ref) {
  const baseClass = 'bg-zinc-950 hover:bg-zinc-900/35'
  // dnd-kit fades the source row to 0 opacity while a DragOverlay
  // renders the clone. Without this the user sees TWO rows during
  // drag (the in-flight ghost and the original).
  const draggingClass = isDragging ? 'opacity-0' : ''
  const overlayClass = isOverlay
    ? 'border border-zinc-700 rounded-md shadow-2xl bg-zinc-900'
    : ''
  return (
    <tr
      ref={ref}
      className={[baseClass, draggingClass, overlayClass, className].filter(Boolean).join(' ')}
      data-testid="collection-task-row"
      data-task-id={task.id}
      {...rest}
    >
      <td
        className="w-6 py-2 pl-3 pr-1 text-zinc-600 cursor-grab active:cursor-grabbing touch-none select-none"
        {...(listeners ?? {})}
        aria-label="Drag to reorder"
      >
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
})

export function CollectionTaskRow({ task, asOverlay = false }: CollectionTaskRowProps) {
  // The DragOverlay clone is rendered outside the SortableContext, so
  // useSortable() must be skipped — it would either error or attach
  // listeners that conflict with the live row's.
  if (asOverlay) {
    return <StaticRowMarkup task={task} isOverlay />
  }

  return <SortableRow task={task} />
}

function SortableRow({ task }: { task: Task }) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: task.id })

  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
  }

  return (
    <StaticRowMarkup
      ref={setNodeRef}
      style={style}
      task={task}
      listeners={listeners}
      isDragging={isDragging}
      // Spreading attributes on the <tr> gives dnd-kit the aria-roledescription
      // and tabIndex needed for keyboard sensor pickup; user tabs to the row,
      // hits Space to grab, arrow keys to move, Space to drop.
      {...attributes}
    />
  )
}
