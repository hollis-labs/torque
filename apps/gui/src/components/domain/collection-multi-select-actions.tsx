import { useState } from 'react'
import { Users, ArrowRight, FolderPlus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
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
import { CollectionCreateDialog } from './collection-create-dialog'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import { STATUS_LABEL, TASK_STATUSES } from '@/lib/constants'
import type { Collection, TaskStatus } from '@/lib/types'

interface CollectionMultiSelectActionsProps {
  selectedTaskIds: string[]
  collections: Collection[]
  currentCollectionId?: string
  onSelectionChange: (selectedIds: string[]) => void
  onTasksChanged: () => void
}

export function CollectionMultiSelectActions({
  selectedTaskIds,
  collections,
  currentCollectionId,
  onSelectionChange,
  onTasksChanged,
}: CollectionMultiSelectActionsProps) {
  const api = useApi()
  const [removeConfirmOpen, setRemoveConfirmOpen] = useState(false)
  const [createCollectionOpen, setCreateCollectionOpen] = useState(false)
  const [pendingNewCollectionTaskIds, setPendingNewCollectionTaskIds] = useState<string[]>([])

  if (selectedTaskIds.length === 0) return null

  async function handleBulkTransition(status: TaskStatus) {
    try {
      await Promise.all(
        selectedTaskIds.map(id => api.transitionTask(id, status))
      )
      notifySuccess(
        `${selectedTaskIds.length} tasks transitioned to ${STATUS_LABEL[status]}`
      )
      onTasksChanged()
      onSelectionChange([])
    } catch (err) {
      notifyError(err, 'Failed to transition tasks')
    }
  }

  async function handleBulkMoveToCollection(collectionId: string) {
    try {
      await Promise.all(
        selectedTaskIds.map(id => api.addTaskToCollection(collectionId, id))
      )
      
      // If we're in a collection view, also remove from current collection
      if (currentCollectionId) {
        await Promise.all(
          selectedTaskIds.map(id => api.removeTaskFromCollection(currentCollectionId, id))
        )
      }

      const collection = collections.find(c => c.id === collectionId)
      notifySuccess(
        `${selectedTaskIds.length} tasks moved to ${collection?.name || 'collection'}`
      )
      onTasksChanged()
      onSelectionChange([])
    } catch (err) {
      notifyError(err, 'Failed to move tasks to collection')
    }
  }

  async function handleRemoveFromCollection() {
    if (!currentCollectionId) return

    try {
      await Promise.all(
        selectedTaskIds.map(id => api.removeTaskFromCollection(currentCollectionId, id))
      )
      notifySuccess(
        `${selectedTaskIds.length} tasks removed from collection`
      )
      onTasksChanged()
      onSelectionChange([])
      setRemoveConfirmOpen(false)
    } catch (err) {
      notifyError(err, 'Failed to remove tasks from collection')
    }
  }

  function handleCreateNewCollection() {
    setPendingNewCollectionTaskIds(selectedTaskIds)
    setCreateCollectionOpen(true)
  }

  async function handleNewCollectionCreated(collection: Collection) {
    if (pendingNewCollectionTaskIds.length === 0) return

    try {
      await Promise.all(
        pendingNewCollectionTaskIds.map(id => api.addTaskToCollection(collection.id, id))
      )
      
      // If we're in a collection view, also remove from current collection
      if (currentCollectionId) {
        await Promise.all(
          pendingNewCollectionTaskIds.map(id => api.removeTaskFromCollection(currentCollectionId, id))
        )
      }

      notifySuccess(
        `${pendingNewCollectionTaskIds.length} tasks moved to ${collection.name}`
      )
      onTasksChanged()
      onSelectionChange([])
    } catch (err) {
      notifyError(err, 'Failed to move tasks to new collection')
    } finally {
      setPendingNewCollectionTaskIds([])
    }
  }

  const availableCollections = collections.filter(c => c.id !== currentCollectionId)

  return (
    <>
      <div className="bg-blue-950/40 border border-blue-800/60 rounded-lg p-3 flex items-center gap-3">
        <div className="flex items-center gap-2 text-blue-200">
          <Users className="h-4 w-4" />
          <span className="text-sm font-medium">
            {selectedTaskIds.length} selected
          </span>
        </div>

        <div className="flex items-center gap-2">
          {/* Transition submenu */}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <ArrowRight className="mr-2 h-3.5 w-3.5" />
                Transition
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              {TASK_STATUSES.map(status => (
                <DropdownMenuItem
                  key={status}
                  onClick={() => handleBulkTransition(status)}
                >
                  {STATUS_LABEL[status]}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>

          {/* Move to collection submenu */}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <FolderPlus className="mr-2 h-3.5 w-3.5" />
                Move to
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem onClick={handleCreateNewCollection}>
                <FolderPlus className="mr-2 h-3.5 w-3.5" />
                Create new collection
              </DropdownMenuItem>
              
              {availableCollections.length > 0 && (
                <>
                  <DropdownMenuSeparator />
                  {availableCollections.map(collection => (
                    <DropdownMenuItem
                      key={collection.id}
                      onClick={() => handleBulkMoveToCollection(collection.id)}
                    >
                      {collection.name}
                    </DropdownMenuItem>
                  ))}
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>

          {/* Remove from current collection */}
          {currentCollectionId && (
            <Button
              variant="outline" 
              size="sm"
              onClick={() => setRemoveConfirmOpen(true)}
              className="text-red-400 hover:text-red-300 border-red-800/60 hover:border-red-700"
            >
              <Trash2 className="mr-2 h-3.5 w-3.5" />
              Remove
            </Button>
          )}

          {/* Clear selection */}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onSelectionChange([])}
          >
            Clear
          </Button>
        </div>
      </div>

      {/* Remove confirmation */}
      <AlertDialog open={removeConfirmOpen} onOpenChange={setRemoveConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove {selectedTaskIds.length} tasks from collection?</AlertDialogTitle>
            <AlertDialogDescription>
              This will remove the selected tasks from the current collection. 
              The tasks themselves will remain in the system.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleRemoveFromCollection}>
              Remove tasks
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Create new collection dialog */}
      <CollectionCreateDialog
        open={createCollectionOpen}
        onOpenChange={setCreateCollectionOpen}
        onCreated={handleNewCollectionCreated}
      />
    </>
  )
}