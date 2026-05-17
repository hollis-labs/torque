import { useState } from 'react'
import { MoreHorizontal, Trash2, Archive, Edit3 } from 'lucide-react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
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
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Collection } from '@/lib/types'

interface CollectionActionsProps {
  collection: Collection
  taskCount: number
  onCollectionChange?: (collection: Collection) => void
  onCollectionDelete?: (id: string) => void
  onClearTasks?: (id: string) => void
}

export function CollectionActions({
  collection,
  taskCount,
  onCollectionChange,
  onCollectionDelete,
  onClearTasks,
}: CollectionActionsProps) {
  const api = useApi()
  const [editOpen, setEditOpen] = useState(false)
  const [clearTasksOpen, setClearTasksOpen] = useState(false)
  const [archiveOpen, setArchiveOpen] = useState(false)
  const [name, setName] = useState(collection.name)
  const [description, setDescription] = useState(collection.description || '')
  const [submitting, setSubmitting] = useState(false)

  async function handleEditSubmit() {
    const trimmedName = name.trim()
    if (!trimmedName) return

    setSubmitting(true)
    try {
      const updated = await api.updateCollection(collection.id, {
        name: trimmedName,
        description: description.trim(),
      })
      notifySuccess('Collection updated')
      onCollectionChange?.(updated)
      setEditOpen(false)
    } catch (err) {
      notifyError(err, 'Failed to update collection')
    } finally {
      setSubmitting(false)
    }
  }

  async function handleClearTasks() {
    try {
      await api.clearCollectionTasks(collection.id)
      notifySuccess('All tasks removed from collection')
      onClearTasks?.(collection.id)
      setClearTasksOpen(false)
    } catch (err) {
      notifyError(err, 'Failed to clear collection tasks')
    }
  }

  async function handleArchive() {
    try {
      await api.archiveCollection(collection.id)
      notifySuccess('Collection archived')
      onCollectionDelete?.(collection.id)
      setArchiveOpen(false)
    } catch (err) {
      notifyError(err, 'Failed to archive collection')
    }
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-7 p-0 text-zinc-400 hover:text-zinc-200"
          >
            <MoreHorizontal className="h-3.5 w-3.5" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={() => setEditOpen(true)}>
            <Edit3 className="mr-2 h-3.5 w-3.5" />
            Edit metadata
          </DropdownMenuItem>
          
          {taskCount > 0 && (
            <DropdownMenuItem onClick={() => setClearTasksOpen(true)}>
              <Trash2 className="mr-2 h-3.5 w-3.5" />
              Clear all tasks
            </DropdownMenuItem>
          )}
          
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => setArchiveOpen(true)}
            className="text-red-400 focus:text-red-300"
          >
            <Archive className="mr-2 h-3.5 w-3.5" />
            Archive collection
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {/* Edit Dialog */}
      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit Collection</DialogTitle>
            <DialogDescription>
              Update the collection name and description.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div>
              <Label htmlFor="edit-name">Name</Label>
              <Input
                id="edit-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Collection name"
              />
            </div>
            <div>
              <Label htmlFor="edit-description">Description (optional)</Label>
              <Textarea
                id="edit-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="Collection description"
                rows={3}
              />
            </div>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setEditOpen(false)}
            >
              Cancel
            </Button>
            <Button
              onClick={handleEditSubmit}
              disabled={submitting || !name.trim()}
            >
              Save changes
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Clear Tasks Confirmation */}
      <AlertDialog open={clearTasksOpen} onOpenChange={setClearTasksOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Clear all tasks from collection?</AlertDialogTitle>
            <AlertDialogDescription>
              This will remove all {taskCount} tasks from "{collection.name}". 
              The tasks themselves will remain in the system, just not part of this collection.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleClearTasks}>
              Clear tasks
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Archive Confirmation */}
      <AlertDialog open={archiveOpen} onOpenChange={setArchiveOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Archive collection?</AlertDialogTitle>
            <AlertDialogDescription>
              This will archive "{collection.name}" and remove all its tasks. 
              Do you also want to clear all tasks from this collection?
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleArchive}>
              Archive collection
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}