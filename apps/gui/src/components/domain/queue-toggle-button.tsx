import { Button } from '@hollis-labs/sysop-ui'
import type { Task, TaskStatus } from '@/lib/types'

const QUEUE_ELIGIBLE_STATUSES: TaskStatus[] = ['backlog', 'todo', 'queued', 'blocked', 'paused']

interface QueueToggleButtonProps {
  task: Task
  busy?: boolean
  onToggle: () => void
}

/**
 * Renders Queue (when manual=true) or Unqueue (when manual=false).
 * Hidden for running/terminal statuses where the manual flag has no effect.
 */
export function QueueToggleButton({ task, busy, onToggle }: QueueToggleButtonProps) {
  if (!QUEUE_ELIGIBLE_STATUSES.includes(task.status)) return null

  const label = task.manual ? 'Queue' : 'Unqueue'
  const aria = task.manual
    ? 'Queue task for scheduler'
    : 'Unqueue task from scheduler'

  return (
    <Button
      size="sm"
      variant="outline"
      onClick={onToggle}
      disabled={busy}
      aria-label={aria}
      data-testid="queue-toggle-button"
      data-manual={String(task.manual)}
      className="text-[11px] h-7 uppercase tracking-[.18em]"
    >
      {busy ? '…' : label}
    </Button>
  )
}
