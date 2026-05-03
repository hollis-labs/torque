import { useState } from 'react'
import { Link } from 'react-router-dom'
import { FolderClosed, FolderPlus, Inbox } from 'lucide-react'
import { QuickAddDialog } from '@/components/collections/QuickAddDialog'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Task } from '@/lib/types'

interface CollectionBadgeProps {
  task: Task
  /** Compact = row-style (smaller text, subtle border). */
  variant?: 'compact' | 'header'
  /**
   * Called after a successful add-to-inbox or modal-driven assignment
   * with the refreshed task. Lets the parent patch optimistic state so
   * the badge re-renders in its new (in-inbox / in-collection) form
   * without a full reload.
   */
  onChange?: (task: Task) => void
}

/**
 * Inline collection badge + quick-action surface.
 *
 *   - In a collection: folder + name, links to /collections.
 *   - In inbox: Inbox label, links to /collections.
 *   - Fresh (never touched): two action icons — "Send to inbox" (skips
 *     the modal and adds directly) and "Add to collection…" (opens the
 *     QuickAddDialog). Both marked data-row-interactive so the
 *     surrounding row click handler doesn't hijack them.
 */
export function CollectionBadge({ task, variant = 'compact', onChange }: CollectionBadgeProps) {
  const inCollection = !!task.collection_id && !!task.collection_name
  const inInbox = !task.collection_id && !!task.added_to_collections_at

  const linkBase =
    variant === 'compact'
      ? 'inline-flex items-center gap-1 rounded border border-zinc-700/80 bg-zinc-900/60 px-1.5 py-px text-[10px] uppercase tracking-[.12em] text-zinc-400 hover:border-zinc-600 hover:text-zinc-200 transition-colors'
      : 'inline-flex items-center gap-1.5 rounded-md border border-zinc-700/80 bg-zinc-900 px-2 py-1 text-[11px] uppercase tracking-[.14em] text-zinc-300 hover:border-zinc-600 hover:text-zinc-100 transition-colors'

  if (inCollection) {
    return (
      <Link
        to="/collections"
        className={linkBase}
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

  if (inInbox) {
    return (
      <Link
        to="/collections"
        className={linkBase}
        title="In inbox (awaiting collection assignment)"
        data-row-interactive="true"
      >
        <Inbox className="h-3 w-3" aria-hidden />
        Inbox
      </Link>
    )
  }

  return <FreshTaskActions task={task} variant={variant} onChange={onChange} />
}

interface FreshTaskActionsProps {
  task: Task
  variant: 'compact' | 'header'
  onChange?: (task: Task) => void
}

function FreshTaskActions({ task, variant, onChange }: FreshTaskActionsProps) {
  const api = useApi()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  const buttonBase =
    variant === 'compact'
      ? 'inline-flex h-5 w-5 items-center justify-center rounded border border-zinc-800 bg-zinc-900/60 text-zinc-500 hover:border-zinc-700 hover:bg-zinc-900 hover:text-zinc-300 disabled:opacity-50 transition-colors'
      : 'inline-flex h-7 w-7 items-center justify-center rounded-md border border-zinc-700/80 bg-zinc-900 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200 disabled:opacity-50 transition-colors'

  const iconClass = variant === 'compact' ? 'h-3 w-3' : 'h-3.5 w-3.5'

  async function handleSendToInbox(e: React.MouseEvent) {
    // Defensive: row click handler honors data-row-interactive via
    // closest(), but the button's also nested inside a <Link> in some
    // contexts — stop propagation so neither outer handler fires.
    e.preventDefault()
    e.stopPropagation()
    if (busy) return
    setBusy(true)
    try {
      await api.addTaskToInbox(task.id)
      notifySuccess('Sent to inbox')
      try {
        const fresh = await api.getTask(task.id)
        onChange?.(fresh)
      } catch {
        // Best-effort refetch — the mutation succeeded; UI will catch
        // up on the next page-level reload.
      }
    } catch (err) {
      notifyError(err, 'Failed to send to inbox')
    } finally {
      setBusy(false)
    }
  }

  function handleOpenDialog(e: React.MouseEvent) {
    e.preventDefault()
    e.stopPropagation()
    setDialogOpen(true)
  }

  return (
    <>
      <span
        className="inline-flex items-center gap-0.5"
        data-row-interactive="true"
      >
        <button
          type="button"
          onClick={handleSendToInbox}
          disabled={busy}
          className={buttonBase}
          title="Send to inbox"
          aria-label="Send to inbox"
        >
          <Inbox className={iconClass} aria-hidden />
        </button>
        <button
          type="button"
          onClick={handleOpenDialog}
          disabled={busy}
          className={buttonBase}
          title="Add to collection…"
          aria-label="Add to collection"
        >
          <FolderPlus className={iconClass} aria-hidden />
        </button>
      </span>
      <QuickAddDialog
        task={task}
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onAssigned={onChange}
      />
    </>
  )
}
