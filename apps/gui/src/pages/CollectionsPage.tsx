import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Archive, Plus, Trash2, FolderX } from 'lucide-react'
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  pointerWithin,
  useDroppable,
  useSensor,
  useSensors,
  type CollisionDetection,
  type DragEndEvent,
  type DragStartEvent,
} from '@dnd-kit/core'
import {
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
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
import { PageHeader } from '@/components/domain/page-header'
import { CollectionCreateDialog } from '@/components/domain/collection-create-dialog'
import { CollectionTaskRow } from '@/components/domain/collection-task-row'
import { CollectionFilter } from '@/components/domain/collection-filter'
import { CollectionMultiSelectActions } from '@/components/domain/collection-multi-select-actions'
import { CollectionActions } from '@/components/domain/collection-actions'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { notifyError, notifySuccess } from '@/lib/toast'
import { DEFAULT_ACTIVE_STATUSES, TASK_STATUSES } from '@/lib/constants'
import type { Collection, Task, TaskStatus } from '@/lib/types'

const SSE_EVENTS = [
  'collection.created',
  'collection.updated',
  'collection.archived',
  'collection.unarchived',
  'collection.task_added',
  'collection.task_removed',
  'collection.task_moved',
  'collection.tasks_reordered',
  'collection.inbox_added',
]

/** Synthetic droppable ID for the inbox container (so dropping on empty space creates a task) */
const INBOX_DROP_ID = '__inbox__'

/** Collision detection for drag-and-drop that prefers droppables over sortables */
const collisionStrategy: CollisionDetection = (args) => {
  // First, try the pointer-within algorithm for droppables
  const pointerCollisions = pointerWithin(args)
  if (pointerCollisions.length > 0) {
    return pointerCollisions
  }
  // Fall back to closest center for sortables
  return closestCenter(args)
}

export default function CollectionsPage() {
  const api = useApi()
  const [collections, setCollections] = useState<Collection[]>([])
  const [loading, setLoading] = useState(true)
  const [createDialogOpen, setCreateDialogOpen] = useState(false)
  const [draggedTask, setDraggedTask] = useState<Task | null>(null)
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false)
  const [collectionToDelete, setCollectionToDelete] = useState<Collection | null>(null)
  
  // Filter state
  const [searchQuery, setSearchQuery] = useState('')
  const [showInbox, setShowInbox] = useState(true)
  const [selectedStatuses, setSelectedStatuses] = useState<TaskStatus[]>(DEFAULT_ACTIVE_STATUSES)
  
  // Multi-select state
  const [selectedTaskIds, setSelectedTaskIds] = useState<string[]>([])
  
  // Clear actions confirmation
  const [clearAllTasksOpen, setClearAllTasksOpen] = useState(false)
  const [clearAllCollectionsOpen, setClearAllCollectionsOpen] = useState(false)

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  )

  const filteredCollections = useMemo(() => {
    let result = collections

    // Filter by search query
    if (searchQuery.trim()) {
      const query = searchQuery.toLowerCase()
      result = result.filter(collection => 
        collection.name.toLowerCase().includes(query) ||
        collection.description?.toLowerCase().includes(query) ||
        collection.tasks.some(task => 
          task.title.toLowerCase().includes(query) ||
          task.description?.toLowerCase().includes(query)
        )
      )
    }

    // Filter collection tasks by status and inbox
    result = result.map(collection => ({
      ...collection,
      tasks: collection.tasks.filter(task => {
        // Status filter
        if (!selectedStatuses.includes(task.status)) return false
        
        // Inbox filter  
        if (!showInbox && task.inbox) return false
        
        return true
      })
    }))

    return result
  }, [collections, searchQuery, selectedStatuses, showInbox])

  const allTasks = useMemo(() => 
    filteredCollections.flatMap(c => c.tasks), 
    [filteredCollections]
  )

  async function fetchCollections() {
    setLoading(true)
    try {
      const data = await api.get<Collection[]>('/collections/with-tasks')
      setCollections(data)
    } catch (error) {
      console.error('Failed to fetch collections:', error)
      notifyError('Failed to load collections')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchCollections()
  }, [api])

  useSSE(SSE_EVENTS, fetchCollections)

  const handleDragStart = useCallback((event: DragStartEvent) => {
    const task = allTasks.find(t => t.id === event.active.id)
    if (task) setDraggedTask(task)
  }, [allTasks])

  const handleDragEnd = useCallback(async (event: DragEndEvent) => {
    setDraggedTask(null)
    
    const { active, over } = event
    if (!over) return

    const activeTaskId = active.id as string
    const overId = over.id as string

    // Handle dropping on inbox
    if (overId === INBOX_DROP_ID) {
      try {
        await api.post(`/collections/inbox/tasks/${activeTaskId}`)
        fetchCollections()
        notifySuccess('Task moved to inbox')
      } catch (error) {
        console.error('Failed to move task to inbox:', error)
        notifyError('Failed to move task to inbox')
      }
      return
    }

    // Handle dropping on collection
    if (overId.startsWith('collection-')) {
      const targetCollectionId = overId.replace('collection-', '')
      try {
        await api.post(`/collections/${targetCollectionId}/tasks/${activeTaskId}`)
        fetchCollections()
        notifySuccess('Task moved to collection')
      } catch (error) {
        console.error('Failed to move task to collection:', error)
        notifyError('Failed to move task to collection')
      }
      return
    }

    // Handle reordering within collection
    const activeTask = allTasks.find(t => t.id === activeTaskId)
    const overTask = allTasks.find(t => t.id === overId)
    
    if (!activeTask || !overTask) return
    if (activeTask.collection_id !== overTask.collection_id) return

    const collection = collections.find(c => c.id === activeTask.collection_id)
    if (!collection) return

    const oldIndex = collection.tasks.findIndex(t => t.id === activeTaskId)
    const newIndex = collection.tasks.findIndex(t => t.id === overId)
    
    if (oldIndex === newIndex) return

    try {
      await api.post(`/collections/${collection.id}/reorder`, {
        task_id: activeTaskId,
        new_position: newIndex,
      })
      fetchCollections()
    } catch (error) {
      console.error('Failed to reorder tasks:', error)
      notifyError('Failed to reorder tasks')
    }
  }, [allTasks, collections, api])

  const handleStatusToggle = useCallback((status: TaskStatus) => {
    setSelectedStatuses(prev => 
      prev.includes(status) 
        ? prev.filter(s => s !== status)
        : [...prev, status]
    )
  }, [])

  const handleTaskTransition = useCallback(async (taskId: string, status: TaskStatus) => {
    try {
      await api.patch(`/tasks/${taskId}`, { status })
      fetchCollections()
      notifySuccess(`Task marked as ${status}`)
    } catch (error) {
      console.error('Failed to transition task:', error)
      notifyError('Failed to update task status')
    }
  }, [api])

  const handleTaskSelect = useCallback((taskId: string, selected: boolean) => {
    setSelectedTaskIds(prev => 
      selected 
        ? [...prev, taskId]
        : prev.filter(id => id !== taskId)
    )
  }, [])

  const handleClearFilters = useCallback(() => {
    setSearchQuery('')
    setSelectedStatuses(DEFAULT_ACTIVE_STATUSES)
    setShowInbox(true)
  }, [])

  const handleClearAllTasks = useCallback(async () => {
    try {
      await api.post('/collections/clear-all-tasks')
      fetchCollections()
      setSelectedTaskIds([])
      notifySuccess('All tasks cleared from collections')
      setClearAllTasksOpen(false)
    } catch (error) {
      console.error('Failed to clear all tasks:', error)
      notifyError('Failed to clear tasks')
    }
  }, [api])

  const handleClearAllCollections = useCallback(async (clearTasks: boolean) => {
    try {
      await api.post('/collections/clear-all', { clear_tasks: clearTasks })
      fetchCollections()
      setSelectedTaskIds([])
      notifySuccess(clearTasks ? 'All collections and tasks cleared' : 'All collections cleared')
      setClearAllCollectionsOpen(false)
    } catch (error) {
      console.error('Failed to clear all collections:', error)
      notifyError('Failed to clear collections')
    }
  }, [api])

  const { setNodeRef: setInboxRef, isOver: inboxIsOver } = useDroppable({
    id: INBOX_DROP_ID,
  })

  function handleDeleteCollection(collection: Collection) {
    setCollectionToDelete(collection)
    setConfirmDeleteOpen(true)
  }

  async function confirmDelete() {
    if (!collectionToDelete) return
    
    try {
      await api.delete(`/collections/${collectionToDelete.id}`)
      fetchCollections()
      notifySuccess('Collection deleted')
    } catch (error) {
      console.error('Failed to delete collection:', error)
      notifyError('Failed to delete collection')
    } finally {
      setConfirmDeleteOpen(false)
      setCollectionToDelete(null)
    }
  }

  if (loading) {
    return (
      <div className="max-w-7xl mx-auto p-6 space-y-8">
        <Skeleton className="h-8 w-48" />
        <div className="space-y-4">
          <Skeleton className="h-32 w-full" />
          <Skeleton className="h-32 w-full" />
          <Skeleton className="h-32 w-full" />
        </div>
      </div>
    )
  }

  return (
    <DndContext 
      sensors={sensors} 
      collisionDetection={collisionStrategy}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
    >
      <div className="max-w-7xl mx-auto p-6 space-y-6">
        {/* Page Header */}
        <PageHeader 
          title="Collections"
          subtitle={`${filteredCollections.length} collection${filteredCollections.length !== 1 ? 's' : ''}, ${allTasks.length} task${allTasks.length !== 1 ? 's' : ''}`}
          action={
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setClearAllTasksOpen(true)}
                className="text-orange-400 border-orange-400/30 hover:bg-orange-400/10"
              >
                <Trash2 className="h-4 w-4 mr-2" />
                Clear All Tasks
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setClearAllCollectionsOpen(true)}
                className="text-red-400 border-red-400/30 hover:bg-red-400/10"
              >
                <FolderX className="h-4 w-4 mr-2" />
                Clear All Collections
              </Button>
              <Button onClick={() => setCreateDialogOpen(true)}>
                <Plus className="h-4 w-4 mr-2" />
                New Collection
              </Button>
            </div>
          }
        />

        {/* Filter Header */}
        <CollectionFilter
          searchQuery={searchQuery}
          onSearchChange={setSearchQuery}
          showInbox={showInbox}
          onInboxToggle={setShowInbox}
          selectedStatuses={selectedStatuses}
          onStatusToggle={handleStatusToggle}
          onClearFilters={handleClearFilters}
        />

        {/* Multi-select Actions */}
        <CollectionMultiSelectActions
          selectedTaskIds={selectedTaskIds}
          collections={collections}
          onSelectionChange={setSelectedTaskIds}
          onTasksChanged={fetchCollections}
        />

        {/* Collections List */}
        <div className="space-y-6">
          {filteredCollections.map(collection => (
            <CollectionCard
              key={collection.id}
              collection={collection}
              collections={collections}
              selectedTaskIds={selectedTaskIds}
              onTaskSelect={handleTaskSelect}
              onTaskTransition={handleTaskTransition}
              onTasksChanged={fetchCollections}
              onCollectionDelete={() => handleDeleteCollection(collection)}
            />
          ))}
        </div>

        {/* Inbox Drop Zone */}
        <div 
          ref={setInboxRef}
          className={`
            min-h-[120px] rounded-lg border-2 border-dashed transition-all
            flex items-center justify-center
            ${inboxIsOver 
              ? 'border-blue-400 bg-blue-400/10 text-blue-400' 
              : 'border-zinc-700 text-zinc-400 hover:border-zinc-600'
            }
          `}
        >
          <div className="text-center">
            <Archive className="h-8 w-8 mx-auto mb-2 opacity-40" />
            <p className="text-sm font-medium">
              {inboxIsOver ? 'Drop to move to inbox' : 'Inbox'}
            </p>
            <p className="text-xs opacity-60">
              Drag tasks here to remove from collections
            </p>
          </div>
        </div>

        {/* Empty State */}
        {filteredCollections.length === 0 && (
          <div className="text-center py-12 text-zinc-400">
            <Archive className="h-12 w-12 mx-auto mb-4 opacity-40" />
            <h3 className="text-lg font-medium mb-2">No collections found</h3>
            <p className="text-sm opacity-60 mb-6">
              {searchQuery ? 'Try adjusting your search criteria' : 'Create your first collection to get started'}
            </p>
            {!searchQuery && (
              <Button onClick={() => setCreateDialogOpen(true)}>
                <Plus className="h-4 w-4 mr-2" />
                Create Collection
              </Button>
            )}
          </div>
        )}
      </div>

      {/* Drag Overlay */}
      <DragOverlay>
        {draggedTask && (
          <div className="bg-zinc-800 border border-zinc-700 rounded-lg p-3 shadow-lg">
            <div className="font-medium text-sm">{draggedTask.title}</div>
            <div className="text-xs text-zinc-400 mt-1">
              Moving task...
            </div>
          </div>
        )}
      </DragOverlay>

      {/* Dialogs */}
      <CollectionCreateDialog
        open={createDialogOpen}
        onOpenChange={setCreateDialogOpen}
        onSuccess={() => {
          fetchCollections()
          setCreateDialogOpen(false)
        }}
      />

      {/* Delete Confirmation */}
      <AlertDialog open={confirmDeleteOpen} onOpenChange={setConfirmDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Collection</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete "{collectionToDelete?.name}"? 
              Tasks in this collection will be moved back to the inbox.
              This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={confirmDelete} className="bg-red-600 hover:bg-red-700">
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Clear All Tasks Confirmation */}
      <AlertDialog open={clearAllTasksOpen} onOpenChange={setClearAllTasksOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Clear All Tasks</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to clear all tasks from all collections? 
              Tasks will be moved back to the inbox. Collections will remain empty but intact.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleClearAllTasks} className="bg-orange-600 hover:bg-orange-700">
              Clear All Tasks
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Clear All Collections Confirmation */}
      <AlertDialog open={clearAllCollectionsOpen} onOpenChange={setClearAllCollectionsOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Clear All Collections</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete all collections? What would you like to do with the tasks?
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction 
              onClick={() => handleClearAllCollections(false)} 
              className="bg-orange-600 hover:bg-orange-700"
            >
              Keep Tasks (move to inbox)
            </AlertDialogAction>
            <AlertDialogAction 
              onClick={() => handleClearAllCollections(true)} 
              className="bg-red-600 hover:bg-red-700"
            >
              Delete All Tasks Too
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </DndContext>
  )
}

interface CollectionCardProps {
  collection: Collection
  collections: Collection[]
  selectedTaskIds: string[]
  onTaskSelect: (taskId: string, selected: boolean) => void
  onTaskTransition: (taskId: string, status: TaskStatus) => void
  onTasksChanged: () => void
  onCollectionDelete: () => void
}

function CollectionCard({ 
  collection, 
  collections,
  selectedTaskIds,
  onTaskSelect,
  onTaskTransition,
  onTasksChanged,
  onCollectionDelete,
}: CollectionCardProps) {
  const { setNodeRef, isOver } = useDroppable({
    id: `collection-${collection.id}`,
  })

  return (
    <div
      ref={setNodeRef} 
      className={`
        bg-zinc-950/80 border rounded-lg overflow-hidden transition-all
        ${isOver 
          ? 'border-blue-400 bg-blue-400/5' 
          : 'border-zinc-800/60 hover:border-zinc-700'
        }
      `}
    >
      {/* Collection Header */}
      <div className="px-4 py-3 border-b border-zinc-800/60">
        <div className="flex items-center justify-between">
          <div>
            <h3 className="font-medium text-lg">{collection.name}</h3>
            {collection.description && (
              <p className="text-sm text-zinc-400 mt-1">{collection.description}</p>
            )}
          </div>
          <div className="flex items-center gap-2">
            <span className="text-sm text-zinc-400">
              {collection.tasks.length} task{collection.tasks.length !== 1 ? 's' : ''}
            </span>
            <CollectionActions
              collection={collection}
              taskCount={collection.tasks.length}
              onCollectionChange={onTasksChanged}
              onCollectionDelete={onCollectionDelete}
              onClearTasks={onTasksChanged}
            />
          </div>
        </div>
      </div>

      {/* Tasks Table - Borderless like BoardPage */}
      {collection.tasks.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-zinc-800/60 text-left">
                <th className="px-4 py-3 text-xs font-medium text-zinc-400 uppercase tracking-wider">
                  <input
                    type="checkbox"
                    checked={collection.tasks.every(task => selectedTaskIds.includes(task.id))}
                    onChange={(e) => {
                      const allSelected = collection.tasks.every(task => selectedTaskIds.includes(task.id))
                      collection.tasks.forEach(task => {
                        onTaskSelect(task.id, !allSelected)
                      })
                    }}
                    className="rounded border-zinc-700"
                  />
                </th>
                <th className="px-4 py-3 text-xs font-medium text-zinc-400 uppercase tracking-wider">Task</th>
                <th className="px-4 py-3 text-xs font-medium text-zinc-400 uppercase tracking-wider">Status</th>
                <th className="px-4 py-3 text-xs font-medium text-zinc-400 uppercase tracking-wider">Priority</th>
                <th className="px-4 py-3 text-xs font-medium text-zinc-400 uppercase tracking-wider">Date</th>
                <th className="px-4 py-3 text-xs font-medium text-zinc-400 uppercase tracking-wider">Actions</th>
              </tr>
            </thead>
            <tbody>
              <SortableContext 
                items={collection.tasks.map(t => t.id)}
                strategy={verticalListSortingStrategy}
              >
                {collection.tasks.map(task => (
                  <CollectionTaskRow
                    key={task.id}
                    task={task}
                    collections={collections}
                    currentCollectionId={collection.id}
                    selected={selectedTaskIds.includes(task.id)}
                    onSelect={onTaskSelect}
                    onTransition={onTaskTransition}
                    onTasksChanged={onTasksChanged}
                  />
                ))}
              </SortableContext>
            </tbody>
          </table>
        </div>
      ) : (
        <div className="px-4 py-8 text-center text-zinc-400">
          <Archive className="h-8 w-8 mx-auto mb-2 opacity-40" />
          <p className="text-sm">
            {isOver ? 'Drop tasks here to add to collection' : 'No tasks in this collection'}
          </p>
        </div>
      )}
    </div>
  )
}