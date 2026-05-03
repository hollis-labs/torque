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
 *   - In inbox: INBOX badge (non-navigating) + FolderPlus button to assign
 *     into a real collection without going through the menu.
 *   - Fresh (never touched): two action icons — "Send to inbox" (skips
 *     the modal and adds directly) and "Add to collection…" (opens the
 *     QuickAddDialog).
 *
 * The inbox + fresh states share the same QuickAddDialog instance via
 * the InboxOrFreshActions sub-component so the FolderPlus button works
 * identically in both. All clickable elements are wrapped in
 * data-row-interactive so the surrounding row click handler doesn't
 * hijack them, and onClick handlers stop propagation defensively.
 */
export function CollectionBadge({ task, variant = 'compact', onChange }: CollectionBadgeProps) {
  const inCollection = !!task.collection_id && !!task.collection_name
  const inInbox = !task.collection_id && !!task.added_to_collections_at

  if (inCollection) {
    const linkBase =
      variant === 'compact'
        ? 'inline-flex items-center gap-1 rounded border border-zinc-700/80 bg-zinc-900/60 px-1.5 py-px text-[10px] uppercase tracking-[.12em] text-zinc-400 hover:border-zinc-600 hover:text-zinc-200 transition-colors'
        : 'inline-flex items-center gap-1.5 rounded-md border border-zinc-700/80 bg-zinc-900 px-2 py-1 text-[11px] uppercase tracking-[.14em] text-zinc-300 hover:border-zinc-600 hover:text-zinc-100 transition-colors'
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

  return <InboxOrFreshActions task={task} variant={variant} onChange={onChange} inInbox={inInbox} />
}

interface InboxOrFreshActionsProps {
  task: Task
  variant: 'compact' | 'header'
  onChange?: (task: Task) => void
  inInbox: boolean
}

function InboxOrFreshActions({ task, variant, onChange, inInbox }: InboxOrFreshActionsProps) {
  const api = useApi()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  const buttonBase =
    variant === 'compact'
      ? 'inline-flex h-5 w-5 items-center justify-center rounded border border-zinc-800 bg-zinc-900/60 text-zinc-500 hover:border-zinc-700 hover:bg-zinc-900 hover:text-zinc-300 disabled:opacity-50 transition-colors'
      : 'inline-flex h-7 w-7 items-center justify-center rounded-md border border-zinc-700/80 bg-zinc-900 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200 disabled:opacity-50 transition-colors'

  const inboxBadgeBase =
    variant === 'compact'
      ? 'inline-flex items-center gap-1 rounded border border-zinc-700/80 bg-zinc-900/60 px-1.5 py-px text-[10px] uppercase tracking-[.12em] text-zinc-400'
      : 'inline-flex items-center gap-1.5 rounded-md border border-zinc-700/80 bg-zinc-900 px-2 py-1 text-[11px] uppercase tracking-[.14em] text-zinc-300'

  const iconClass = variant === 'compact' ? 'h-3 w-3' : 'h-3.5 w-3.5'

  async function handleSendToInbox(e: React.MouseEvent) {
    // Prevent navigation/row-click side effects: the button is wrapped in
    // data-row-interactive on the row side, but this also guards against
    // any ancestor click handler treating the action as a link.
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
        // Best-effort refetch — the mutation succeeded; UI catches up
        // on the next page-level reload.
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
        {inInbox ? (
          // Already in inbox — show a non-navigating badge so clicks
          // don't whisk the user away to /collections (their explicit
          // request: "adding to inbox should stay on the same page").
          <span
            className={inboxBadgeBase}
            title="In inbox (awaiting collection assignment)"
          >
            <Inbox className={iconClass} aria-hidden />
            Inbox
          </span>
        ) : (
          // Fresh task — direct send-to-inbox shortcut.
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
        )}
        <button
          type="button"
          onClick={handleOpenDialog}
          disabled={busy}
          className={buttonBase}
          title={inInbox ? 'Move to collection…' : 'Add to collection…'}
          aria-label={inInbox ? 'Move to collection' : 'Add to collection'}
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
