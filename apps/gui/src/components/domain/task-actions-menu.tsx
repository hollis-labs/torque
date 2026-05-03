import { useState, type ReactNode } from 'react'
import { MoreHorizontal } from 'lucide-react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { QuickAddDialog } from '@/components/collections/QuickAddDialog'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import { STATUS_LABEL, TASK_STATUSES } from '@/lib/constants'
import type { Task, TaskStatus } from '@/lib/types'

interface TaskActionsMenuProps {
  task: Task
  onChange?: (task: Task) => void
  onDelete?: (id: string) => void
  align?: 'start' | 'end'
  triggerClassName?: string
  triggerContent?: ReactNode
  triggerAriaLabel?: string
}

const DEFAULT_TRIGGER_CLASS =
  'inline-flex h-7 w-7 items-center justify-center rounded-md border border-zinc-700/80 bg-zinc-900 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200 transition-colors'

export function TaskActionsMenu({
  task,
  onChange,
  onDelete,
  align = 'end',
  triggerClassName = DEFAULT_TRIGGER_CLASS,
  triggerContent = <MoreHorizontal className="h-3.5 w-3.5" />,
  triggerAriaLabel = 'Task actions',
}: TaskActionsMenuProps) {
  const api = useApi()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [quickAddOpen, setQuickAddOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [forcePrompt, setForcePrompt] = useState<{
    target: TaskStatus
    reason: string
  } | null>(null)

  const isReview = task.status === 'review'
  const isTerminal = task.status === 'done' || task.status === 'archived'
  const isTodoManual = task.status === 'todo' && task.manual

  const showApprove = isReview
  const showMarkDone = !isTerminal
  const showMoveToTodo = task.status !== 'todo'
  const showMoveToBacklog = !isTerminal && !isTodoManual
  const showPause = !isTerminal && task.status !== 'paused'

  // Submenu lists every status except the current one. Force-bypass is wired
  // up so users can disposition stuck tasks even when the FSM rejects.
  const transitionTargets = TASK_STATUSES.filter((s) => s !== task.status)

  async function runTransition(status: string, successMsg: string) {
    try {
      const updated = await api.transitionTask(task.id, status)
      onChange?.(updated)
      notifySuccess(successMsg)
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to update status'
      // FSM rejections come back as 422 — offer the force-bypass instead of
      // burying the rule violation in a terse toast.
      if (looksLikeFSMRejection(message)) {
        setForcePrompt({ target: status as TaskStatus, reason: message })
        return
      }
      notifyError(err, 'Failed to update status')
    }
  }

  async function handleForceConfirmed() {
    if (!forcePrompt) return
    setBusy(true)
    try {
      const updated = await api.transitionTask(task.id, forcePrompt.target, { force: true })
      onChange?.(updated)
      notifySuccess(`Forced to ${STATUS_LABEL[forcePrompt.target] ?? forcePrompt.target}`)
      setForcePrompt(null)
    } catch (err) {
      notifyError(err, 'Failed to force transition')
    } finally {
      setBusy(false)
    }
  }

  async function handleMoveToBacklog() {
    try {
      let current = task
      if (current.status !== 'todo') {
        current = await api.transitionTask(task.id, 'todo')
      }
      if (!current.manual) {
        current = await api.updateTask(task.id, { manual: true })
      }
      onChange?.(current)
      notifySuccess('Moved to backlog')
    } catch (err) {
      notifyError(err, 'Failed to move to backlog')
    }
  }

  async function handleDeleteConfirmed() {
    setBusy(true)
    try {
      await api.deleteTask(task.id)
      notifySuccess('Task deleted')
      setConfirmOpen(false)
      onDelete?.(task.id)
    } catch (err) {
      notifyError(err, 'Failed to delete task')
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <span data-row-interactive="true">
        <DropdownMenu>
          <DropdownMenuTrigger
            className={triggerClassName}
            aria-label={triggerAriaLabel}
            data-testid="task-row-actions-trigger"
          >
            {triggerContent}
          </DropdownMenuTrigger>
          {/* data-row-interactive on the portaled content too — synthetic event
              bubbling from menu items reaches the parent <tr> in BoardPage,
              and its row-click handler checks via DOM closest(). Without this,
              clicking any item that opens a dialog fires row navigation first. */}
          <DropdownMenuContent
            align={align}
            className="min-w-44"
            data-row-interactive="true"
          >
            {showApprove && (
              <DropdownMenuItem onClick={() => runTransition('done', 'Approved')}>
                Approve
              </DropdownMenuItem>
            )}
            {showMarkDone && (
              <DropdownMenuItem onClick={() => runTransition('done', 'Marked done')}>
                Mark done
              </DropdownMenuItem>
            )}
            {showMoveToTodo && (
              <DropdownMenuItem onClick={() => runTransition('todo', 'Moved to todo')}>
                Move to todo
              </DropdownMenuItem>
            )}
            {showMoveToBacklog && (
              <DropdownMenuItem onClick={handleMoveToBacklog}>
                Move to backlog
              </DropdownMenuItem>
            )}
            {showPause && (
              <DropdownMenuItem onClick={() => runTransition('paused', 'Paused')}>
                Pause
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => setQuickAddOpen(true)}>
              Add to collection…
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>Transition to…</DropdownMenuSubTrigger>
              <DropdownMenuSubContent>
                {transitionTargets.map((s) => (
                  <DropdownMenuItem
                    key={s}
                    onClick={() => runTransition(s, `Moved to ${STATUS_LABEL[s] ?? s}`)}
                  >
                    {STATUS_LABEL[s] ?? s}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuSubContent>
            </DropdownMenuSub>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              variant="destructive"
              onClick={() => setConfirmOpen(true)}
            >
              Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </span>

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this task?</AlertDialogTitle>
            <AlertDialogDescription>
              This permanently deletes{' '}
              <span className="font-mono text-zinc-300">{task.title}</span>{' '}
              and its comments, runs, and artifacts. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={handleDeleteConfirmed}
              disabled={busy}
            >
              {busy ? 'Deleting…' : 'Delete'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <QuickAddDialog
        task={task}
        open={quickAddOpen}
        onOpenChange={setQuickAddOpen}
        onAssigned={(updated) => onChange?.(updated)}
      />

      <AlertDialog
        open={forcePrompt !== null}
        onOpenChange={(open) => !open && setForcePrompt(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Force transition?</AlertDialogTitle>
            <AlertDialogDescription>
              The FSM rejected this move:{' '}
              <span className="font-mono text-zinc-300">{forcePrompt?.reason}</span>
              <br />
              Forcing skips the lifecycle rules and writes{' '}
              <span className="font-mono text-zinc-300">
                {forcePrompt && (STATUS_LABEL[forcePrompt.target] ?? forcePrompt.target)}
              </span>{' '}
              directly. Use for cleanup of stuck tasks.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleForceConfirmed} disabled={busy}>
              {busy ? 'Forcing…' : 'Force anyway'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

// looksLikeFSMRejection inspects an error string for the conflict markers the
// service.TransitionError produces. Conservative — only triggers the force
// prompt for transition-rule errors, not network or unrelated 422s.
function looksLikeFSMRejection(message: string): boolean {
  const m = message.toLowerCase()
  return m.includes('transition not permitted') || m.includes('unknown source status')
}
