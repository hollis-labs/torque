import { Link } from 'react-router-dom'
import { FolderClosed, Inbox } from 'lucide-react'
import type { Task } from '@/lib/types'

interface CollectionBadgeProps {
  task: Task
  /** Compact = row-style (smaller text, subtle border). */
  variant?: 'compact' | 'header'
}

/**
 * Inline badge surfacing a task's collection membership for review at a
 * glance. Renders nothing for fresh tasks (never touched by collections),
 * a folder + name for assigned tasks, and an Inbox marker for tasks added
 * to the collections view but not yet sorted.
 *
 * Click navigates to /collections so the user can jump to the dashboard.
 * Marked data-row-interactive so the surrounding task-row click handler
 * doesn't hijack the navigation.
 */
export function CollectionBadge({ task, variant = 'compact' }: CollectionBadgeProps) {
  const inCollection = !!task.collection_id && !!task.collection_name
  const inInbox = !task.collection_id && !!task.added_to_collections_at

  if (!inCollection && !inInbox) {
    return null
  }

  const base =
    variant === 'compact'
      ? 'inline-flex items-center gap-1 rounded border border-zinc-700/80 bg-zinc-900/60 px-1.5 py-px text-[10px] uppercase tracking-[.12em] text-zinc-400 hover:border-zinc-600 hover:text-zinc-200 transition-colors'
      : 'inline-flex items-center gap-1.5 rounded-md border border-zinc-700/80 bg-zinc-900 px-2 py-1 text-[11px] uppercase tracking-[.14em] text-zinc-300 hover:border-zinc-600 hover:text-zinc-100 transition-colors'

  if (inCollection) {
    return (
      <Link
        to="/collections"
        className={base}
        title={`In collection: ${task.collection_name}`}
        data-row-interactive="true"
      >
        <FolderClosed className="h-3 w-3" aria-hidden />
        <span className="truncate max-w-[12rem] normal-case tracking-normal text-zinc-300">
          {task.collection_name}
        </span>
      </Link>
    )
  }

  return (
    <Link
      to="/collections"
      className={base}
      title="In inbox (awaiting collection assignment)"
      data-row-interactive="true"
    >
      <Inbox className="h-3 w-3" aria-hidden />
      Inbox
    </Link>
  )
}
