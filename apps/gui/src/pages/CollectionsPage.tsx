import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Archive, Plus } from 'lucide-react'
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

/** MIME-ish key passed through dataTransfer. We stash a JSON blob keyed
 * by this so other drag sources on the page (e.g. browser file drops)
 * are easy to disambiguate. */
const DRAG_MIME = 'application/x-clockwork-task'

interface DragPayload {
  taskId: string
  sourceCollectionId: string | null // null = inbox
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

interface DropIndicatorState {
  rowTaskId: string
  position: 'before' | 'after'
}

interface CollectionTaskTableProps {
  /** null = inbox; string = collection id. */
  collectionKey: string | null
  tasks: Task[]
  emptyHint: string
  onDropTask: (
    payload: DragPayload,
    target: { collectionId: string | null; relTaskId: string | null; position: 'before' | 'after' | 'end' },
  ) => void
}

/**
 * Owns the drag/drop wiring for one container (inbox or a single
 * collection). Empty containers still need to be drop targets so the
 * user can drop a task into a blank collection — handled by an
 * always-rendered "empty drop zone" footer row.
 */
function CollectionTaskTable({
  collectionKey,
  tasks,
  emptyHint,
  onDropTask,
}: CollectionTaskTableProps) {
  const [dropIndicator, setDropIndicator] = useState<DropIndicatorState | null>(null)
  const [containerHover, setContainerHover] = useState(false)

  function readPayload(e: React.DragEvent): DragPayload | null {
    try {
      const raw = e.dataTransfer.getData(DRAG_MIME)
      if (!raw) return null
      return JSON.parse(raw) as DragPayload
    } catch {
      return null
    }
  }

  function handleDragStart(e: React.DragEvent<HTMLTableRowElement>, taskId: string) {
    const payload: DragPayload = {
      taskId,
      sourceCollectionId: collectionKey,
    }
    e.dataTransfer.setData(DRAG_MIME, JSON.stringify(payload))
    e.dataTransfer.effectAllowed = 'move'
  }

  function handleRowDragOver(e: React.DragEvent<HTMLTableRowElement>, taskId: string) {
    if (!e.dataTransfer.types.includes(DRAG_MIME)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
    const rect = e.currentTarget.getBoundingClientRect()
    const midpoint = rect.top + rect.height / 2
    const position: 'before' | 'after' = e.clientY < midpoint ? 'before' : 'after'
    setDropIndicator((prev) =>
      prev?.rowTaskId === taskId && prev.position === position ? prev : { rowTaskId: taskId, position }
    )
  }

  function handleRowDrop(e: React.DragEvent<HTMLTableRowElement>, taskId: string) {
    if (!e.dataTransfer.types.includes(DRAG_MIME)) return
    e.preventDefault()
    e.stopPropagation()
    const payload = readPayload(e)
    setDropIndicator(null)
    setContainerHover(false)
    if (!payload) return
    const rect = e.currentTarget.getBoundingClientRect()
    const midpoint = rect.top + rect.height / 2
    const position: 'before' | 'after' = e.clientY < midpoint ? 'before' : 'after'
    onDropTask(payload, {
      collectionId: collectionKey,
      relTaskId: taskId,
      position,
    })
  }

  function handleContainerDragOver(e: React.DragEvent<HTMLDivElement>) {
    if (!e.dataTransfer.types.includes(DRAG_MIME)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
    setContainerHover(true)
  }

  function handleContainerDragLeave(e: React.DragEvent<HTMLDivElement>) {
    // Only clear when leaving the container itself, not its children.
    if (e.currentTarget.contains(e.relatedTarget as Node | null)) return
    setContainerHover(false)
  }

  function handleContainerDrop(e: React.DragEvent<HTMLDivElement>) {
    if (!e.dataTransfer.types.includes(DRAG_MIME)) return
    e.preventDefault()
    setContainerHover(false)
    setDropIndicator(null)
    const payload = readPayload(e)
    if (!payload) return
    // No row target → drop at end.
    onDropTask(payload, {
      collectionId: collectionKey,
      relTaskId: null,
      position: 'end',
    })
  }

  function handleRowDragLeave() {
    // Cleared on next dragover; no-op here keeps the indicator stable
    // while moving across rows.
  }

  function handleRowDragEnd() {
    setDropIndicator(null)
  }

  return (
    <div
      className={`mx-4 mb-4 rounded-md border ${containerHover ? 'border-zinc-500 bg-zinc-900/30' : 'border-zinc-800/80 bg-zinc-950'}`}
      onDragOver={handleContainerDragOver}
      onDragLeave={handleContainerDragLeave}
      onDrop={handleContainerDrop}
    >
      {tasks.length === 0 ? (
        <div className="px-4 py-6 text-center text-[12px] text-zinc-600 italic">
          {emptyHint}
        </div>
      ) : (
        <table className="min-w-full">
          <tbody className="divide-y divide-zinc-800/60">
            {tasks.map((task) => (
              <CollectionTaskRow
                key={task.id}
                task={task}
                onDragStart={handleDragStart}
                onDragOver={handleRowDragOver}
                onDragLeave={handleRowDragLeave}
                onDrop={handleRowDrop}
                onDragEnd={handleRowDragEnd}
                dropIndicator={
                  dropIndicator?.rowTaskId === task.id ? dropIndicator.position : null
                }
              />
            ))}
          </tbody>
        </table>
      )}
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

  /**
   * The single drop dispatcher. Translates a (source, target) pair into
   * the smallest-correct API call:
   *
   *   - reorder within the same collection → reorderCollectionTasks
   *   - source inbox → target collection   → addTaskToCollection
   *   - source collection → target inbox   → removeTaskFromCollection
   *   - source A → target B (both real)    → moveTask
   *
   * Optimistic update on the local state; refetch on error so the user
   * sees the canonical order from the server.
   */
  const handleDropTask = useCallback(
    (
      payload: DragPayload,
      target: { collectionId: string | null; relTaskId: string | null; position: 'before' | 'after' | 'end' },
    ) => {
      const { taskId, sourceCollectionId } = payload
      const sameContainer = sourceCollectionId === target.collectionId

      // ---- helper: compute target index in the destination list ------
      const destList: Task[] =
        target.collectionId === null
          ? inboxTasks
          : (tasksByCollection[target.collectionId] ?? [])
      let destIndex: number
      if (target.position === 'end' || target.relTaskId === null) {
        destIndex = destList.length
      } else {
        const i = destList.findIndex((t) => t.id === target.relTaskId)
        if (i < 0) destIndex = destList.length
        else destIndex = target.position === 'before' ? i : i + 1
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
        // When dragging downward, removing the source first shifts the
        // target index down by one — adjust before splicing back in.
        let to = destIndex
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
      // Optimistic source removal:
      const sourceList: Task[] =
        sourceCollectionId === null
          ? inboxTasks
          : (tasksByCollection[sourceCollectionId] ?? [])
      const moved = sourceList.find((t) => t.id === taskId)
      if (!moved) return

      const newSource = sourceList.filter((t) => t.id !== taskId)
      const newDest = [...destList]
      newDest.splice(destIndex, 0, moved)

      // Apply optimistic state.
      if (sourceCollectionId === null) {
        setInboxTasks(newSource)
      } else {
        setTasksByCollection((prev) => ({ ...prev, [sourceCollectionId]: newSource }))
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

      if (sourceCollectionId !== null && target.collectionId === null) {
        // collection → inbox
        api.removeTaskFromCollection(sourceCollectionId, taskId).catch(onError)
      } else if (sourceCollectionId === null && target.collectionId !== null) {
        // inbox → collection
        api.moveTask(taskId, target.collectionId, destIndex).catch(onError)
      } else if (target.collectionId !== null) {
        // collection A → collection B
        api.moveTask(taskId, target.collectionId, destIndex).catch(onError)
      }
    },
    [api, inboxTasks, tasksByCollection, load],
  )

  const sortedCollections = useMemo(
    () => [...collections].sort((a, b) => a.created_at.localeCompare(b.created_at)),
    [collections],
  )

  return (
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
              onDropTask={handleDropTask}
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
                onDropTask={handleDropTask}
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
  )
}

