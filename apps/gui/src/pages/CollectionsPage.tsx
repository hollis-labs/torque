import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Archive, Plus } from 'lucide-react'
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
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Collection, Task } from '@/lib/types'

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

/** Synthetic droppable ID for the inbox container (so dropping on empty
 * inbox space hits a target without needing a row to overlap). */
const INBOX_CONTAINER_ID = 'container:inbox'

/** Prefix for per-collection container droppables. The id encodes the
 * collection id so onDragEnd can recover it without a separate map. */
function collectionContainerID(collectionId: string): string {
  return `container:${collectionId}`
}

/** Inverse of collectionContainerID. Returns null if the id isn't a
 * container token. Returns the synthetic "inbox" sentinel for the
 * inbox. */
function parseContainerID(id: string): { kind: 'container'; collectionId: string | null } | null {
  if (id === INBOX_CONTAINER_ID) return { kind: 'container', collectionId: null }
  if (id.startsWith('container:')) return { kind: 'container', collectionId: id.slice('container:'.length) }
  return null
}

function PageSkeleton() {
  return (
    <div className="flex flex-col gap-4 p-6">
      <Skeleton className="h-32 w-full rounded-2xl" />
      <Skeleton className="h-32 w-full rounded-2xl" />
    </div>
  )
}

interface CollectionHeaderProps {
  collection: Collection
  taskCount: number
  onUpdate: (id: string, fields: { name?: string; description?: string }) => Promise<void>
  onArchive: (id: string) => void
}

function CollectionHeader({ collection, taskCount, onUpdate, onArchive }: CollectionHeaderProps) {
  // Drafts re-seed from the canonical collection whenever editing
  // begins. When NOT editing, the displayed value is read directly
  // from `collection`, so SSE / sibling-session edits flow through
  // automatically. This avoids the React 19 setState-in-effect
  // anti-pattern that an `editing` + sync-effect pair would create.
  const [editingName, setEditingName] = useState(false)
  const [editingDesc, setEditingDesc] = useState(false)
  const [draftName, setDraftName] = useState(collection.name)
  const [draftDesc, setDraftDesc] = useState(collection.description)

  function startEditingName() {
    setDraftName(collection.name)
    setEditingName(true)
  }
  function startEditingDesc() {
    setDraftDesc(collection.description)
    setEditingDesc(true)
  }

  async function commitName() {
    const trimmed = draftName.trim()
    setEditingName(false)
    if (!trimmed || trimmed === collection.name) {
      setDraftName(collection.name)
      return
    }
    try {
      await onUpdate(collection.id, { name: trimmed })
    } catch {
      setDraftName(collection.name)
    }
  }

  async function commitDesc() {
    const trimmed = draftDesc.trim()
    setEditingDesc(false)
    if (trimmed === collection.description) return
    try {
      await onUpdate(collection.id, { description: trimmed })
    } catch {
      setDraftDesc(collection.description)
    }
  }

  return (
    <div className="flex items-start justify-between gap-3 px-4 pt-4 pb-2">
      <div className="min-w-0 flex-1">
        {editingName ? (
          <Input
            autoFocus
            value={draftName}
            onChange={(e) => setDraftName(e.target.value)}
            onBlur={commitName}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                ;(e.target as HTMLInputElement).blur()
              }
              if (e.key === 'Escape') {
                e.preventDefault()
                setDraftName(collection.name)
                setEditingName(false)
              }
            }}
            className="h-8 text-base font-semibold"
          />
        ) : (
          <button
            type="button"
            onClick={startEditingName}
            className="text-left text-base font-semibold text-zinc-100 hover:text-zinc-50"
            title="Click to rename"
          >
            {collection.name}
            <span className="ml-2 text-[11px] font-normal text-zinc-500">
              {taskCount} {taskCount === 1 ? 'task' : 'tasks'}
            </span>
          </button>
        )}
        {editingDesc ? (
          <Textarea
            autoFocus
            value={draftDesc}
            onChange={(e) => setDraftDesc(e.target.value)}
            onBlur={commitDesc}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.preventDefault()
                setDraftDesc(collection.description)
                setEditingDesc(false)
              }
              // Cmd/Ctrl-Enter commits; bare Enter inserts a newline
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                e.preventDefault()
                ;(e.target as HTMLTextAreaElement).blur()
              }
            }}
            rows={2}
            className="mt-1 text-[12px]"
            placeholder="Description"
          />
        ) : (
          <button
            type="button"
            onClick={startEditingDesc}
            className="mt-1 block text-left text-[12px] text-zinc-400 hover:text-zinc-300"
            title="Click to edit description"
          >
            {collection.description || (
              <span className="italic text-zinc-600">Add description…</span>
            )}
          </button>
        )}
      </div>
      <Button
        variant="ghost"
        size="sm"
        className="text-zinc-400 hover:text-zinc-200"
        onClick={() => onArchive(collection.id)}
        aria-label={`Archive collection ${collection.name}`}
        title="Archive collection"
      >
        <Archive className="h-4 w-4" />
      </Button>
    </div>
  )
}

interface CollectionTaskTableProps {
  /** null = inbox; string = collection id. Used to construct the
   * container droppable id and (via the page-level lookups) recover
   * which container a drop landed in. */
  collectionKey: string | null
  tasks: Task[]
  emptyHint: string
}

/**
 * Renders one container's rows inside a SortableContext. The whole
 * container (rounded box) is registered as a droppable too — this is
 * what makes empty containers accept drops, and what gives a "drop at
 * end" target when the user releases over blank space inside a
 * non-empty container.
 *
 * Drag/drop intent is owned by the page; this component only renders.
 */
function CollectionTaskTable({ collectionKey, tasks, emptyHint }: CollectionTaskTableProps) {
  const containerId = collectionKey === null ? INBOX_CONTAINER_ID : collectionContainerID(collectionKey)
  const { setNodeRef, isOver } = useDroppable({ id: containerId })

  return (
    <div
      ref={setNodeRef}
      className={`mx-4 mb-4 rounded-md border ${isOver ? 'border-zinc-500 bg-zinc-900/30' : 'border-zinc-800/80 bg-zinc-950'}`}
      data-container-id={containerId}
    >
      <SortableContext items={tasks.map((t) => t.id)} strategy={verticalListSortingStrategy}>
        {tasks.length === 0 ? (
          <div className="px-4 py-6 text-center text-[12px] text-zinc-600 italic">
            {emptyHint}
          </div>
        ) : (
          <table className="min-w-full">
            <tbody className="divide-y divide-zinc-800/60">
              {tasks.map((task) => (
                <CollectionTaskRow key={task.id} task={task} />
              ))}
            </tbody>
          </table>
        )}
      </SortableContext>
    </div>
  )
}

export default function CollectionsPage() {
  const api = useApi()
  const { lastEvent } = useSSE(SSE_EVENTS)
  const loadGeneration = useRef(0)
  const [collections, setCollections] = useState<Collection[]>([])
  const [inboxTasks, setInboxTasks] = useState<Task[]>([])
  const [tasksByCollection, setTasksByCollection] = useState<Record<string, Task[]>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [archiveTarget, setArchiveTarget] = useState<Collection | null>(null)
  const [archiving, setArchiving] = useState(false)
  /** id of the task currently mid-drag, used to render the DragOverlay
   * clone. null when no drag is in flight. */
  const [activeDragTaskId, setActiveDragTaskId] = useState<string | null>(null)

  const sensors = useSensors(
    // 5px activation distance — prevents click-on-row from registering
    // as a drag, which would block the title <Link> from navigating.
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const load = useCallback(async () => {
    const myGen = ++loadGeneration.current
    setLoading(true)
    setError(null)
    try {
      const [active, inbox] = await Promise.all([
        api.listCollections('active'),
        api.listInboxTasks(),
      ])
      if (myGen !== loadGeneration.current) return
      const taskMap: Record<string, Task[]> = {}
      // Fan out task fetches so each collection populates independently;
      // cheaper than a single mega-endpoint and matches the backend
      // shape (collection list and task list are separate resources).
      const taskResults = await Promise.all(
        active.map((c) => api.listCollectionTasks(c.id).then((t) => [c.id, t] as const)),
      )
      if (myGen !== loadGeneration.current) return
      for (const [id, t] of taskResults) {
        taskMap[id] = t
      }
      setCollections(active)
      setInboxTasks(inbox)
      setTasksByCollection(taskMap)
    } catch (err) {
      if (myGen !== loadGeneration.current) return
      setError(err instanceof Error ? err.message : 'Failed to load collections')
    } finally {
      if (myGen === loadGeneration.current) setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])

  // SSE refetch — collections fire a wide event surface and the refetch
  // is cheap (one collections list + one inbox + N per-collection task
  // lists, all parallel), so a blanket reload keeps the page honest
  // without optimistic-state bookkeeping for events from sibling
  // sessions.
  useEffect(() => {
    if (!lastEvent) return
    void load()
  }, [lastEvent, load])

  const handleUpdateCollection = useCallback(
    async (id: string, fields: { name?: string; description?: string }) => {
      try {
        const updated = await api.updateCollection(id, fields)
        setCollections((prev) => prev.map((c) => (c.id === id ? updated : c)))
        notifySuccess('Collection updated')
      } catch (err) {
        notifyError(err, 'Failed to update collection')
        throw err
      }
    },
    [api],
  )

  function requestArchive(id: string) {
    const c = collections.find((c) => c.id === id)
    if (c) setArchiveTarget(c)
  }

  async function confirmArchive() {
    if (!archiveTarget) return
    setArchiving(true)
    try {
      await api.archiveCollection(archiveTarget.id)
      notifySuccess(`Archived ${archiveTarget.name}`)
      setArchiveTarget(null)
      void load()
    } catch (err) {
      notifyError(err, 'Failed to archive collection')
    } finally {
      setArchiving(false)
    }
  }

  /** Resolve which container ("inbox" or a collection id) holds the
   * given task right now. Returns undefined if the task isn't in any
   * tracked container — shouldn't happen in normal flow. */
  const findContainerForTask = useCallback(
    (taskId: string): { collectionId: string | null } | undefined => {
      if (inboxTasks.some((t) => t.id === taskId)) return { collectionId: null }
      for (const [colId, tasks] of Object.entries(tasksByCollection)) {
        if (tasks.some((t) => t.id === taskId)) return { collectionId: colId }
      }
      return undefined
    },
    [inboxTasks, tasksByCollection],
  )

  /** Resolve a `over.id` (either a task id or a `container:` token)
   * down to a target container + relative task id. The container is
   * always known; the relTaskId is non-null only when the user
   * released over a specific row. */
  const resolveDropTarget = useCallback(
    (overId: string): { collectionId: string | null; relTaskId: string | null } | null => {
      const asContainer = parseContainerID(overId)
      if (asContainer) {
        return { collectionId: asContainer.collectionId, relTaskId: null }
      }
      // overId is a task id — find which container holds it.
      const where = findContainerForTask(overId)
      if (!where) return null
      return { collectionId: where.collectionId, relTaskId: overId }
    },
    [findContainerForTask],
  )

  /**
   * Custom collision detection: prefer pointer-within (so the user's
   * cursor position decides), fall back to closest-center for keyboard
   * sensor (which lacks a pointer). Without this, dropping on the
   * empty space of a container can match an unrelated row above it.
   */
  const collisionDetection: CollisionDetection = useCallback(
    (args) => {
      const pointerCollisions = pointerWithin(args)
      if (pointerCollisions.length > 0) return pointerCollisions
      return closestCenter(args)
    },
    [],
  )

  const handleDragStart = useCallback((event: DragStartEvent) => {
    setActiveDragTaskId(String(event.active.id))
  }, [])

  /**
   * The single drop dispatcher. Translates a (source, target) pair into
   * the smallest-correct API call:
   *
   *   - reorder within the same collection → reorderCollectionTasks
   *   - source inbox → target collection   → moveTask
   *   - source collection → target inbox   → removeTaskFromCollection
   *   - source A → target B (both real)    → moveTask
   *
   * Optimistic update on the local state; refetch on error so the user
   * sees the canonical order from the server.
   */
  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      setActiveDragTaskId(null)
      const { active, over } = event
      if (!over) return

      const taskId = String(active.id)
      const overId = String(over.id)

      const source = findContainerForTask(taskId)
      if (!source) return

      const target = resolveDropTarget(overId)
      if (!target) return

      const sameContainer = source.collectionId === target.collectionId

      // Compute target index in the destination list. When dropping on
      // a row, place AFTER it (matches the dnd-kit sortable convention
      // where releasing on a row swaps positions). When dropping on a
      // container with no row target, append.
      const destList: Task[] =
        target.collectionId === null
          ? inboxTasks
          : (tasksByCollection[target.collectionId] ?? [])

      let destIndex: number
      if (target.relTaskId === null) {
        destIndex = destList.length
      } else {
        const i = destList.findIndex((t) => t.id === target.relTaskId)
        destIndex = i < 0 ? destList.length : i
      }

      // ---- 1. same container reorder --------------------------------
      if (sameContainer) {
        if (target.collectionId === null) {
          // Inbox is virtual — reordering within the inbox has no
          // backend representation in v1. Bail silently; UI keeps the
          // server order on next refetch.
          return
        }
        const list = [...destList]
        const fromIdx = list.findIndex((t) => t.id === taskId)
        if (fromIdx < 0) return
        let to = destIndex
        // When dragging downward, removing the source first shifts the
        // target index down by one — adjust before splicing back in.
        if (fromIdx < to) to -= 1
        if (fromIdx === to) return
        const [moved] = list.splice(fromIdx, 1)
        list.splice(to, 0, moved)
        setTasksByCollection((prev) => ({ ...prev, [target.collectionId as string]: list }))
        api
          .reorderCollectionTasks(target.collectionId as string, list.map((t) => t.id))
          .catch((err) => {
            notifyError(err, 'Failed to reorder')
            void load()
          })
        return
      }

      // ---- 2. cross-container moves ---------------------------------
      const sourceList: Task[] =
        source.collectionId === null
          ? inboxTasks
          : (tasksByCollection[source.collectionId] ?? [])
      const moved = sourceList.find((t) => t.id === taskId)
      if (!moved) return

      const newSource = sourceList.filter((t) => t.id !== taskId)
      const newDest = [...destList]
      newDest.splice(destIndex, 0, moved)

      // Apply optimistic state.
      if (source.collectionId === null) {
        setInboxTasks(newSource)
      } else {
        setTasksByCollection((prev) => ({ ...prev, [source.collectionId as string]: newSource }))
      }
      if (target.collectionId === null) {
        setInboxTasks(newDest)
      } else {
        setTasksByCollection((prev) => ({ ...prev, [target.collectionId as string]: newDest }))
      }

      // Pick the right backend call.
      const onError = (err: unknown) => {
        notifyError(err, 'Failed to move task')
        void load()
      }

      if (source.collectionId !== null && target.collectionId === null) {
        // collection → inbox
        api.removeTaskFromCollection(source.collectionId, taskId).catch(onError)
      } else if (target.collectionId !== null) {
        // inbox → collection OR collection A → collection B
        api.moveTask(taskId, target.collectionId, destIndex).catch(onError)
      }
    },
    [api, inboxTasks, tasksByCollection, load, findContainerForTask, resolveDropTarget],
  )

  const handleDragCancel = useCallback(() => {
    setActiveDragTaskId(null)
  }, [])

  const sortedCollections = useMemo(
    () => [...collections].sort((a, b) => a.created_at.localeCompare(b.created_at)),
    [collections],
  )

  const activeDragTask = useMemo<Task | null>(() => {
    if (!activeDragTaskId) return null
    if (inboxTasks) {
      const inboxHit = inboxTasks.find((t) => t.id === activeDragTaskId)
      if (inboxHit) return inboxHit
    }
    for (const tasks of Object.values(tasksByCollection)) {
      const hit = tasks.find((t) => t.id === activeDragTaskId)
      if (hit) return hit
    }
    return null
  }, [activeDragTaskId, inboxTasks, tasksByCollection])

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={collisionDetection}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      onDragCancel={handleDragCancel}
    >
      <div className="flex h-full min-h-0 flex-col">
        <PageHeader title="Collections">
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="mr-1 h-3.5 w-3.5" />
            New collection
          </Button>
        </PageHeader>

        {loading && collections.length === 0 ? (
          <PageSkeleton />
        ) : error ? (
          <div className="m-6 rounded-md border border-rose-900/60 bg-rose-950/30 p-4 text-[13px] text-rose-300">
            {error}
          </div>
        ) : (
          <div className="flex-1 overflow-auto">
            <section className="pb-2 pt-4">
              <div className="flex items-baseline justify-between gap-3 px-4 pb-2">
                <div>
                  <h2 className="text-base font-semibold text-zinc-100">Inbox</h2>
                  <p className="text-[12px] text-zinc-500">tasks awaiting organization</p>
                </div>
                <span className="text-[11px] text-zinc-500">
                  {inboxTasks.length} {inboxTasks.length === 1 ? 'task' : 'tasks'}
                </span>
              </div>
              <CollectionTaskTable
                collectionKey={null}
                tasks={inboxTasks}
                emptyHint="Inbox is empty. Drop tasks here to clear collection assignment."
              />
            </section>

            {sortedCollections.map((collection) => (
              <section key={collection.id} className="border-t border-zinc-800/60">
                <CollectionHeader
                  collection={collection}
                  taskCount={tasksByCollection[collection.id]?.length ?? 0}
                  onUpdate={handleUpdateCollection}
                  onArchive={requestArchive}
                />
                <CollectionTaskTable
                  collectionKey={collection.id}
                  tasks={tasksByCollection[collection.id] ?? []}
                  emptyHint="Drop tasks here to add them to this collection."
                />
              </section>
            ))}

            {sortedCollections.length === 0 && (
              <div className="mx-4 my-6 rounded-md border border-dashed border-zinc-800 px-6 py-10 text-center text-[13px] text-zinc-500">
                No active collections yet. Create one to start organizing tasks.
              </div>
            )}
          </div>
        )}

        <CollectionCreateDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          onCreated={() => void load()}
        />

        <AlertDialog open={archiveTarget !== null} onOpenChange={(open) => !open && setArchiveTarget(null)}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Archive this collection?</AlertDialogTitle>
              <AlertDialogDescription>
                Archiving{' '}
                <span className="font-mono text-zinc-300">{archiveTarget?.name}</span>{' '}
                hides it from the active list. Tasks already in it stay assigned;
                you can unarchive later via the API if needed.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={archiving}>Cancel</AlertDialogCancel>
              <AlertDialogAction onClick={confirmArchive} disabled={archiving}>
                {archiving ? 'Archiving…' : 'Archive'}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>

      {/* DragOverlay renders the drag preview as a positioned clone
          following the pointer. Wrapping the row clone in a table is
          required because <tr> can't be a top-level child outside a
          table — without this the browser unwraps the row and the
          overlay is blank. */}
      <DragOverlay>
        {activeDragTask ? (
          <table className="min-w-full">
            <tbody>
              <CollectionTaskRow task={activeDragTask} asOverlay />
            </tbody>
          </table>
        ) : null}
      </DragOverlay>
    </DndContext>
  )
}
