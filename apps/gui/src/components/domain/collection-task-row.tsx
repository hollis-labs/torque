import { forwardRef, useState, type CSSProperties, type HTMLAttributes } from 'react'
import { GripVertical, FolderOpen, MoreHorizontal, Folder, ArrowRight } from 'lucide-react'
import { Link, useNavigate } from 'react-router-dom'
import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { DraggableSyntheticListeners } from '@dnd-kit/core'
import { StatusBadge } from './status-badge'
import { PriorityBadge } from './priority-badge'
import { CopyableId } from './copyable-id'
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
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import { STATUS_LABEL, TASK_STATUSES } from '@/lib/constants'
import { formatRelativeTime } from '@/lib/utils'
import type { Task, Collection, TaskStatus } from '@/lib/types'

/**
 * Enhanced row for the Collections page with full action menu support.
 * Now includes status transitions, collection moving, task removal, and
 * folder icon for quick collection changes. Still supports drag-and-drop
 * via `@dnd-kit/sortable`.
 */
interface CollectionTaskRowProps {
  task: Task
  collections: Collection[]
  currentCollectionId?: string
  selected?: boolean
  onSelect?: (id: string, selected: boolean) => void
  onTransition?: (id: string, status: TaskStatus) => void
  onTasksChanged?: () => void
}

// Any descendant marked `data-row-interactive="true"` owns its own click
// handling and must not trigger row-level navigation. The row click handler
// uses `closest()` to honor the flag for any nested target.
const INTERACTIVE_SELECTOR = '[data-row-interactive="true"]'

export const CollectionTaskRow = forwardRef<HTMLTableRowElement, CollectionTaskRowProps>(
  function CollectionTaskRow({ 
    task, 
    collections, 
    currentCollectionId,
    selected = false,
    onSelect,
    onTransition,
    onTasksChanged,
  }, ref) {
    const api = useApi()
    const navigate = useNavigate()
    const [removeConfirmOpen, setRemoveConfirmOpen] = useState(false)
    
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
      opacity: isDragging ? 0.5 : 1,
    }

    const dateStr = task.updated_at
      ? new Date(task.updated_at).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
      : '-'
    const agoStr = task.updated_at ? formatRelativeTime(task.updated_at) : ''

    // Filter out current collection and inbox from move options
    const availableCollections = collections.filter(c => 
      c.id !== currentCollectionId && !c.archived
    )

    function handleRowClick(e: React.MouseEvent<HTMLTableRowElement>) {
      // Only the primary button should navigate. Let modifier-clicks and
      // middle-clicks fall through so they can't hijack "open in new tab"
      if (e.button !== 0) return

      // Skip navigation if an interactive element was clicked
      if ((e.target as Element).closest?.(INTERACTIVE_SELECTOR)) return

      // Navigate to task detail
      navigate(`/tasks/${task.id}`)
    }

    async function handleRemoveFromCollection() {
      if (!currentCollectionId) return
      
      try {
        await api.delete(`/collections/${currentCollectionId}/tasks/${task.id}`)
        notifySuccess('Task removed from collection')
        onTasksChanged?.()
        setRemoveConfirmOpen(false)
      } catch (error) {
        console.error('Failed to remove task from collection:', error)
        notifyError('Failed to remove task from collection')
      }
    }

    async function handleMoveToCollection(targetCollectionId: string) {
      try {
        // First remove from current collection if in one
        if (currentCollectionId) {
          await api.delete(`/collections/${currentCollectionId}/tasks/${task.id}`)
        }
        
        // Then add to target collection
        await api.post(`/collections/${targetCollectionId}/tasks/${task.id}`)
        
        const targetCollection = collections.find(c => c.id === targetCollectionId)
        notifySuccess(`Task moved to ${targetCollection?.name || 'collection'}`)
        onTasksChanged?.()
      } catch (error) {
        console.error('Failed to move task to collection:', error)
        notifyError('Failed to move task to collection')
      }
    }

    async function handleMoveToInbox() {
      if (!currentCollectionId) return
      
      try {
        await api.delete(`/collections/${currentCollectionId}/tasks/${task.id}`)
        notifySuccess('Task moved to inbox')
        onTasksChanged?.()
      } catch (error) {
        console.error('Failed to move task to inbox:', error)
        notifyError('Failed to move task to inbox')
      }
    }

    return (
      <>
        <tr
          ref={(node) => {
            setNodeRef(node)
            if (typeof ref === 'function') ref(node)
            else if (ref) ref.current = node
          }}
          style={style}
          onClick={handleRowClick}
          className={`
            cursor-pointer hover:bg-zinc-800/40 transition-colors group
            ${selected ? 'bg-blue-600/10 border-l-2 border-l-blue-500' : ''}
            ${isDragging ? 'opacity-50' : ''}
          `}
        >
          {/* Drag Handle & Checkbox */}
          <td className="px-4 py-3 w-12">
            <div className="flex items-center gap-2" data-row-interactive="true">
              <input
                type="checkbox"
                checked={selected}
                onChange={(e) => onSelect?.(task.id, e.target.checked)}
                className="rounded border-zinc-700"
              />
              <button
                {...attributes}
                {...listeners}
                className="opacity-0 group-hover:opacity-100 transition-opacity text-zinc-400 hover:text-zinc-300"
              >
                <GripVertical className="h-4 w-4" />
              </button>
            </div>
          </td>

          {/* Task Title & ID */}
          <td className="px-4 py-3">
            <div className="space-y-1">
              <div className="flex items-center gap-2">
                {task.inbox && (
                  <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-green-500/20 text-green-400 border border-green-500/30">
                    inbox
                  </span>
                )}
                <span className="font-medium text-zinc-100 group-hover:text-blue-400 transition-colors">
                  {task.title}
                </span>
              </div>
              <CopyableId id={task.id} />
            </div>
          </td>

          {/* Status */}
          <td className="px-4 py-3">
            <StatusBadge status={task.status} />
          </td>

          {/* Priority */}
          <td className="px-4 py-3">
            <PriorityBadge priority={task.priority} />
          </td>

          {/* Date */}
          <td className="px-4 py-3 text-sm text-zinc-400">
            <div className="space-y-1">
              <div>{dateStr}</div>
              {agoStr && <div className="text-xs opacity-60">{agoStr}</div>}
            </div>
          </td>

          {/* Actions */}
          <td className="px-4 py-3 w-24">
            <div className="flex items-center gap-1" data-row-interactive="true">
              {/* Collection Move (Folder Icon) */}
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button className="p-1.5 rounded hover:bg-zinc-700/50 text-zinc-400 hover:text-zinc-300 transition-colors">
                    <Folder className="h-4 w-4" />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-48">
                  <DropdownMenuItem onClick={handleMoveToInbox}>
                    <ArrowRight className="h-4 w-4 mr-2" />
                    Move to Inbox
                  </DropdownMenuItem>
                  {availableCollections.length > 0 && (
                    <>
                      <DropdownMenuSeparator />
                      {availableCollections.map(collection => (
                        <DropdownMenuItem 
                          key={collection.id}
                          onClick={() => handleMoveToCollection(collection.id)}
                        >
                          <FolderOpen className="h-4 w-4 mr-2" />
                          {collection.name}
                        </DropdownMenuItem>
                      ))}
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>

              {/* Main Actions Menu */}
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button className="p-1.5 rounded hover:bg-zinc-700/50 text-zinc-400 hover:text-zinc-300 transition-colors">
                    <MoreHorizontal className="h-4 w-4" />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-48">
                  {/* Status Transitions */}
                  <DropdownMenuSub>
                    <DropdownMenuSubTrigger>
                      <ArrowRight className="h-4 w-4 mr-2" />
                      Change Status
                    </DropdownMenuSubTrigger>
                    <DropdownMenuSubContent>
                      {TASK_STATUSES.filter(status => status !== task.status).map(status => (
                        <DropdownMenuItem 
                          key={status}
                          onClick={() => onTransition?.(task.id, status)}
                        >
                          {STATUS_LABEL[status]}
                        </DropdownMenuItem>
                      ))}
                    </DropdownMenuSubContent>
                  </DropdownMenuSub>

                  <DropdownMenuSeparator />

                  {/* Collection Management */}
                  <DropdownMenuSub>
                    <DropdownMenuSubTrigger>
                      <FolderOpen className="h-4 w-4 mr-2" />
                      Move to Collection
                    </DropdownMenuSubTrigger>
                    <DropdownMenuSubContent>
                      <DropdownMenuItem onClick={handleMoveToInbox}>
                        <ArrowRight className="h-4 w-4 mr-2" />
                        Inbox
                      </DropdownMenuItem>
                      {availableCollections.length > 0 && (
                        <>
                          <DropdownMenuSeparator />
                          {availableCollections.map(collection => (
                            <DropdownMenuItem 
                              key={collection.id}
                              onClick={() => handleMoveToCollection(collection.id)}
                            >
                              <FolderOpen className="h-4 w-4 mr-2" />
                              {collection.name}
                            </DropdownMenuItem>
                          ))}
                        </>
                      )}
                    </DropdownMenuSubContent>
                  </DropdownMenuSub>

                  {currentCollectionId && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem 
                        onClick={() => setRemoveConfirmOpen(true)}
                        className="text-red-400 focus:text-red-400"
                      >
                        Remove from Collection
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </td>
        </tr>

        {/* Remove Confirmation Dialog */}
        <AlertDialog open={removeConfirmOpen} onOpenChange={setRemoveConfirmOpen}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Remove Task from Collection</AlertDialogTitle>
              <AlertDialogDescription>
                Are you sure you want to remove "{task.title}" from this collection?
                The task will be moved back to the inbox.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction 
                onClick={handleRemoveFromCollection}
                className="bg-red-600 hover:bg-red-700"
              >
                Remove
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </>
    )
  }
)