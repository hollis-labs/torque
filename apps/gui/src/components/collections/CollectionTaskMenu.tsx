import { MoreHorizontal } from 'lucide-react'
import { Button, DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubTrigger, DropdownMenuSubContent } from '@hollis-labs/sysop-ui'
import { TASK_STATUSES, STATUS_LABEL } from '@/lib/constants'
import type { Task, TaskStatus } from '@/lib/types'

export function CollectionTaskMenu({ task, disabled, onStatus, onMove, onInbox, onRemove }: {
  task: Task; disabled: boolean; onStatus: (status: TaskStatus) => void
  onMove: () => void; onInbox: () => void; onRemove: () => void
}) {
  return <DropdownMenu>
    <DropdownMenuTrigger render={<Button variant="ghost" size="sm" />} disabled={disabled} aria-label={`Actions for ${task.title}`}><MoreHorizontal className="h-4 w-4" /></DropdownMenuTrigger>
    <DropdownMenuContent align="end">
      <DropdownMenuSub>
        <DropdownMenuSubTrigger>Change status</DropdownMenuSubTrigger>
        <DropdownMenuSubContent>{TASK_STATUSES.map(status => <DropdownMenuItem key={status} disabled={status === task.status} onClick={() => onStatus(status)}>{STATUS_LABEL[status]}</DropdownMenuItem>)}</DropdownMenuSubContent>
      </DropdownMenuSub>
      <DropdownMenuItem onClick={onMove}>Move to collection…</DropdownMenuItem>
      <DropdownMenuItem onClick={onInbox}>Move to inbox</DropdownMenuItem>
      {task.collection_id && <><DropdownMenuSeparator /><DropdownMenuItem onClick={onRemove}>Remove from collection</DropdownMenuItem></>}
    </DropdownMenuContent>
  </DropdownMenu>
}
