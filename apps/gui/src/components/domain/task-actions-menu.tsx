import { useState, type ReactNode, type SyntheticEvent } from 'react'
import { MoreHorizontal } from 'lucide-react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
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
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Task } from '@/lib/types'

interface TaskActionsMenuProps {
  task: Task
  onChange?: (task: Task) => void
  onDelete?: (id: string) => void
  align?: 'start' | 'end'
  triggerClassName?: string
  triggerContent?: ReactNode
  triggerAriaLabel?: string
}

function stop(e: SyntheticEvent) {
  e.stopPropagation()
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
  const [busy, setBusy] = useState(false)

  const isReview = task.status === 'review'
  const isTerminal = task.status === 'done' || task.status === 'archived'
  const isTodoManual = task.status === 'todo' && task.manual

  const showApprove = isReview
  const showMarkDone = !isTerminal
  const showMoveToTodo = task.status !== 'todo'
  const showMoveToBacklog = !isTerminal && !isTodoManual
  const showPause = !isTerminal && task.status !== 'paused'

  async function runTransition(status: string, successMsg: string) {
    try {
      const updated = await api.transitionTask(task.id, status)
      onChange?.(updated)
      notifySuccess(successMsg)
    } catch (err) {
      notifyError(err, 'Failed to update status')
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
      <span onClick={stop} onKeyDown={stop}>
        <DropdownMenu>
          <DropdownMenuTrigger
            className={triggerClassName}
            aria-label={triggerAriaLabel}
          >
            {triggerContent}
          </DropdownMenuTrigger>
          <DropdownMenuContent align={align} className="min-w-44">
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
    </>
  )
}
