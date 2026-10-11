import { CSS } from '@dnd-kit/utilities'
import { useSortable } from '@dnd-kit/sortable'
import { GripVertical, MessageSquare } from 'lucide-react'
import { PlannerStatus } from '@/components/planner/planner-status'
import { itemFacts } from '@/lib/planner'
import type { PlannerDb, PlannerItem } from '@/lib/types'

interface PlannerRowProps {
  db: PlannerDb
  item: PlannerItem
  /** Drag to reorder; off while the list is filtered. */
  sortable: boolean
  onOpen: () => void
}

export function PlannerRow({ db, item, sortable, onOpen }: PlannerRowProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: item.id, disabled: !sortable })
  const facts = itemFacts(db, item)
  const comments = item.comments?.length ?? 0

  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={`flex items-start gap-2 border-b border-border px-4 py-3 ${isDragging ? 'bg-muted opacity-70' : 'hover:bg-muted/50'}`}
    >
      <button
        type="button"
        className={`mt-0.5 shrink-0 cursor-grab touch-none text-muted-foreground ${sortable ? '' : 'invisible'}`}
        aria-label={`Drag to reorder ${item.id}`}
        {...attributes}
        {...listeners}
        tabIndex={sortable ? 0 : -1}
      >
        <GripVertical className="h-4 w-4" />
      </button>
      <button type="button" className="flex min-w-0 flex-1 flex-col gap-1 text-left" onClick={onOpen} aria-label={`Open ${item.id}: ${item.title}`}>
        <span className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-xs text-muted-foreground">{item.id}</span>
          <span className="font-medium">{item.title}</span>
          {facts.map((f) => (
            <span key={f} className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
              {f}
            </span>
          ))}
        </span>
        {item.tldr && <span className="line-clamp-2 text-sm text-muted-foreground">{item.tldr}</span>}
      </button>
      <span className="flex shrink-0 items-center gap-3">
        {comments > 0 && (
          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground" title={`${comments} comments`}>
            <MessageSquare className="h-3.5 w-3.5" />
            {comments}
          </span>
        )}
        <PlannerStatus status={item.status} />
      </span>
    </li>
  )
}
