import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { MoreHorizontal, Plus } from 'lucide-react'
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
import { Skeleton, Button, Input, Textarea, PageHeader, DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from '@hollis-labs/sysop-ui'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@hollis-labs/sysop-ui'
import { CollectionCreateDialog } from '@/components/domain/collection-create-dialog'
import { CollectionTaskRow } from '@/components/domain/collection-task-row'
import { useApi } from '@/hooks/use-api'
import { usePagedList } from '@/hooks/use-paged-list'
import { ListPageControls } from '@/components/domain/list-page-controls'
import { useListSearch } from '@/hooks/use-list-search'
import { runCollectionActions } from '@/lib/collection-actions'
import { CollectionTaskMenu } from '@/components/collections/CollectionTaskMenu'
import { CollectionMoveDialog } from '@/components/collections/CollectionMoveDialog'
import { TASK_STATUSES, STATUS_LABEL } from '@/lib/constants'
import { useSSE } from '@/hooks/use-sse'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Collection, Task, TaskStatus } from '@/lib/types'

const SSE_EVENTS = [
  'task.updated',
  'task.transitioned',
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
  onUnarchive: () => void
  onClear: () => void
  clearDisabledReason?: string
  busy: boolean
}

function CollectionHeader({ collection, taskCount, onUpdate, onArchive, onUnarchive, onClear, clearDisabledReason, busy }: CollectionHeaderProps) {
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
              {taskCount} loaded tasks
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
      <DropdownMenu>
        <DropdownMenuTrigger render={<Button variant="ghost" size="sm" />} disabled={busy} aria-label={`Actions for collection ${collection.name}`}><MoreHorizontal className="h-4 w-4" /></DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={() => { startEditingName(); startEditingDesc() }}>Edit metadata</DropdownMenuItem>
          {collection.archived_at ? <DropdownMenuItem onClick={onUnarchive}>Unarchive collection</DropdownMenuItem> : <DropdownMenuItem onClick={() => onArchive(collection.id)}>Archive collection</DropdownMenuItem>}
          <DropdownMenuSeparator />
          <DropdownMenuItem disabled={!!clearDisabledReason} onClick={onClear}>Clear collection tasks</DropdownMenuItem>
          {clearDisabledReason && <p className="max-w-64 px-2 py-1 text-xs text-muted-foreground">{clearDisabledReason}</p>}
        </DropdownMenuContent>
      </DropdownMenu>
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
  rowControls: (task: Task, key: string | null) => { leading: ReactNode; trailing: ReactNode; draggable: boolean }
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
function CollectionTaskTable({ collectionKey, tasks, emptyHint, rowControls }: CollectionTaskTableProps) {
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
                <CollectionTaskRow key={task.id} task={task} {...rowControls(task, collectionKey)} />
              ))}
            </tbody>
          </table>
        )}
      </SortableContext>
    </div>
  )
}

/** Each mounted collection owns one cursor chain; no page-wide task fan-out. */
function PagedCollectionTasks({ collectionKey, tasks, emptyHint, generation, report, filters, rowControls }: {
  collectionKey: string | null; tasks: Task[]; emptyHint: string; generation: number
  report: (key: string | null, tasks: Task[], hasMore: boolean, queryKey: string) => void
  filters: { search: string; status: TaskStatus | undefined; sort_by: string; sort_dir: 'asc' | 'desc' }
  rowControls: CollectionTaskTableProps['rowControls']
}) {
  const api = useApi()
  const cohortKey = JSON.stringify({ filters, generation })
  const fetchedCohort = useRef('')
  const page = usePagedList({
    queryKey: `${collectionKey ?? 'inbox'}:${generation}`,
    params: { ...filters, include_total: true },
    fetchPage: async ({ params, cursor, signal }) => {
      const result = collectionKey === null
        ? await api.listInboxTasks({ ...params, cursor }, signal)
        : await api.listCollectionTasks(collectionKey, { ...params, cursor }, signal)
      if (!signal.aborted) fetchedCohort.current = cohortKey
      return result
    },
    getId: task => task.id,
  })
  useEffect(() => { report(collectionKey, page.items, page.hasMore || page.loading || !page.meta || !!page.error || page.isStale || fetchedCohort.current !== cohortKey, cohortKey) }, [collectionKey, page.items, page.hasMore, page.loading, page.meta, page.error, page.isStale, cohortKey, report])
  return <>
    <CollectionTaskTable collectionKey={collectionKey} tasks={tasks} rowControls={rowControls} emptyHint={page.loading ? 'Loading tasks…' : emptyHint} />
    <ListPageControls page={page} label="tasks" />
  </>
}

export default function CollectionsPage() {
  const api = useApi()
  const { lastEvent } = useSSE(SSE_EVENTS)
  const [generation, setGeneration] = useState(0)
  const [collectionSearch, setCollectionSearch] = useState('')
  const [collectionStatus, setCollectionStatus] = useState<'active' | 'archived' | 'all'>('active')
  const [taskSearch, setTaskSearch] = useState('')
  const [taskStatus, setTaskStatus] = useState<TaskStatus | undefined>()
  const [taskSort, setTaskSort] = useState('position')
  const [taskDir, setTaskDir] = useState<'asc' | 'desc'>('asc')
  const search = useListSearch(collectionSearch)
  const filteredTaskSearch = useListSearch(taskSearch)
  const [selected, setSelected] = useState<Record<string, Task>>({})
  const [actionBusy, setActionBusy] = useState(false)
  const [actionResult, setActionResult] = useState<{ label: string; succeeded: string[]; failed: { id: string; message: string }[]; notStarted: string[] } | null>(null)
  const [moveIds, setMoveIds] = useState<string[] | null>(null)
  const [removeTarget, setRemoveTarget] = useState<{ name: string; rows: Task[]; collectionId?: string; cohortKey?: string } | null>(null)
  const [acknowledgedEvent, setAcknowledgedEvent] = useState(lastEvent)
  const eventRef = useRef(lastEvent)
  useEffect(() => { eventRef.current = lastEvent }, [lastEvent])
  const stale = lastEvent !== acknowledgedEvent
  const filters = { search: filteredTaskSearch, status: taskStatus, sort_by: taskSort, sort_dir: taskDir }
  const taskCohortKey = JSON.stringify({ filters, generation })
  const filtersPending = taskSearch.trim() !== filteredTaskSearch || collectionSearch.trim() !== search
  const canDrag = !taskSearch.trim() && !taskStatus && taskSort === 'position' && taskDir === 'asc' && !actionBusy && !stale && collectionStatus === 'active'

  const collectionsPage = usePagedList({
    params: { search, status: collectionStatus, sort_by: 'created_at', sort_dir: 'asc' as const, include_total: true },
    fetchPage: ({ params, cursor, signal }) => api.listCollections(params.status, { search: params.search, sort_by: params.sort_by, sort_dir: params.sort_dir, include_total: params.include_total, cursor }, signal),
    getId: collection => collection.id,
  })
  const { items: collections, loading, error, reload: reloadCollections, applyEvent: updateCollectionRow } = collectionsPage
  const [moreByCollection, setMoreByCollection] = useState<Record<string, { blocked: boolean; queryKey: string }>>({})
  const [inboxTasks, setInboxTasks] = useState<Task[]>([])
  const [tasksByCollection, setTasksByCollection] = useState<Record<string, Task[]>>({})
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
    setGeneration(value => value + 1)
    setSelected({})
    setAcknowledgedEvent(eventRef.current)
    await reloadCollections()
  }, [reloadCollections])

  const reportTasks = useCallback((key: string | null, rows: Task[], hasMore: boolean, queryKey: string) => {
    if (key === null) setInboxTasks(rows)
    else {
      setTasksByCollection(previous => previous[key] === rows ? previous : { ...previous, [key]: rows })
      setMoreByCollection(previous => previous[key]?.blocked === hasMore && previous[key]?.queryKey === queryKey ? previous : { ...previous, [key]: { blocked: hasMore, queryKey } })
    }
  }, [])

  async function applyAction(label: string, ids: string[], action: (id: string) => Promise<void>) {
    if (actionBusy || stale || filtersPending) return
    setActionBusy(true)
    setActionResult(null)
    try {
      const result = await runCollectionActions(ids, action)
      setActionResult({ label, ...result })
      if (!result.failed.length) notifySuccess(`${label}: ${result.succeeded.length} ${label.startsWith('Remove') ? 'removed' : 'succeeded'}`)
      else notifyError(new Error(`${label}: ${result.succeeded.length} ${label.startsWith('Remove') ? 'removed' : 'succeeded'}, ${result.failed.length} failed, ${result.notStarted.length} not attempted. See details on the page.`), label)
      await load()
    } finally { setActionBusy(false) }
  }

  const selectedRows = Object.values(selected)
  const selectedIds = selectedRows.map(task => task.id)
  const selectedMembers = selectedRows.filter(task => task.collection_id)
  function clearReason(id: string) {
    if (actionBusy || loading || filtersPending || stale) return 'Refresh the loaded pages and wait for loading to finish.'
    if (taskSearch.trim() || taskStatus) return 'Reset task filters before clearing this collection.'
    if (moreByCollection[id]?.blocked !== false || moreByCollection[id]?.queryKey !== taskCohortKey) return 'Load remaining tasks before clearing this collection.'
    if (!(tasksByCollection[id]?.length)) return 'This collection has no tasks.'
    return undefined
  }
  function rowControls(task: Task, key: string | null) {
    const scoped = { ...task, collection_id: key }
    return {
      draggable: canDrag,
      leading: <input type="checkbox" aria-label={`Select ${task.title}`} checked={!!selected[task.id]} disabled={actionBusy || filtersPending} onChange={event => setSelected(previous => {
        const next = { ...previous }
        if (event.target.checked) next[task.id] = scoped
        else delete next[task.id]
        return next
      })} />,
      trailing: <CollectionTaskMenu task={scoped} disabled={actionBusy || filtersPending || stale} onStatus={status => void applyAction('Change status', [task.id], async id => { await api.transitionTask(id, status) })} onMove={() => setMoveIds([task.id])} onInbox={() => void applyAction('Move to inbox', [task.id], id => api.addTaskToInbox(id))} onRemove={() => setRemoveTarget({ name: collections.find(collection => collection.id === key)?.name ?? 'collection', rows: [scoped] })} />,
    }
  }

  async function confirmRemove() {
    if (!removeTarget || actionBusy || stale || (removeTarget.collectionId && (clearReason(removeTarget.collectionId) || removeTarget.cohortKey !== taskCohortKey))) return
    const target = removeTarget
    await applyAction('Remove collection assignments', target.rows.map(task => task.id), id => {
      const task = target.rows.find(row => row.id === id)!
      return api.removeTaskFromCollection(task.collection_id!, id)
    })
    setRemoveTarget(null)
  }

  const handleUpdateCollection = useCallback(
    async (id: string, fields: { name?: string; description?: string }) => {
      try {
        const updated = await api.updateCollection(id, fields)
        updateCollectionRow({ id, item: updated })
        notifySuccess('Collection updated')
      } catch (err) {
        notifyError(err, 'Failed to update collection')
        throw err
      }
    },
    [api, updateCollectionRow],
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
      for (const collection of collections) {
        if (tasksByCollection[collection.id]?.some(task => task.id === taskId)) return { collectionId: collection.id }
      }
      return undefined
    },
    [inboxTasks, collections, tasksByCollection],
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

      if (!canDrag) return
      const source = findContainerForTask(taskId)
      if (!source) return

      const target = resolveDropTarget(overId)
      if (!target) return

      // Partial reorders would assign new positions over unseen rows. Require a complete destination.
      if (target.collectionId !== null && (moreByCollection[target.collectionId]?.blocked !== false || moreByCollection[target.collectionId]?.queryKey !== taskCohortKey)) {
        notifyError(new Error('Load the remaining destination tasks before reordering or dropping here.'), 'Collection has more tasks')
        return
      }
      const sameContainer = source.collectionId === target.collectionId

      // Compute target index in the destination list. When dropping on
      // a row, place AT that row's index — splice(index, 0, moved) puts
      // the moved task BEFORE the target row, pushing it down by one.
      // When dropping on a container with no row target, append.
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
        // collection → inbox: scoped DELETE so the route is authoritative
        // (rejects 409 if the task isn't actually in source.collectionId).
        api.removeTaskFromCollection(source.collectionId, taskId).catch(onError)
      } else if (target.collectionId !== null) {
        // inbox → collection OR collection A → collection B.
        //
        // Two-step to keep server-side ordering bulletproof:
        //   1. moveTask with no explicit position → backend appends
        //      (max(collection_position)+1 in the target collection).
        //   2. reorderCollectionTasks with the full destination order
        //      atomically rewrites collection_position 1..N matching
        //      the optimistic UI.
        //
        // Why not pass `destIndex` directly to moveTask? Two reasons:
        //   - destIndex is 0-based; the backend's `position` field is
        //     1-based with `<=0` meaning append. The math mismatch alone
        //     would silently land tasks in the wrong slot.
        //   - MoveTaskToCollection SETs the position without shifting
        //     other rows, so even with correct math, an explicit
        //     position would collide with an existing task at that
        //     slot. ORDER BY tie-broke on created_at, and the visual
        //     order drifted on the next refetch. The reorder step
        //     rewrites the whole collection's positions atomically.
        const targetCollectionID = target.collectionId
        const orderedIDs = newDest.map((t) => t.id)
        api
          .moveTask(taskId, targetCollectionID)
          .then(() =>
            api.reorderCollectionTasks(targetCollectionID, orderedIDs),
          )
          .catch(onError)
      }
    },
    [api, inboxTasks, tasksByCollection, moreByCollection, taskCohortKey, canDrag, load, findContainerForTask, resolveDropTarget],
  )

  const handleDragCancel = useCallback(() => {
    setActiveDragTaskId(null)
  }, [])

  const sortedCollections = collections

  const activeDragTask = useMemo<Task | null>(() => {
    if (!activeDragTaskId) return null
    if (inboxTasks) {
      const inboxHit = inboxTasks.find((t) => t.id === activeDragTaskId)
      if (inboxHit) return inboxHit
    }
    for (const collection of collections) {
      const hit = tasksByCollection[collection.id]?.find(task => task.id === activeDragTaskId)
      if (hit) return hit
    }
    return null
  }, [activeDragTaskId, inboxTasks, collections, tasksByCollection])

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

        <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
          <Input aria-label="Search collections" placeholder="Search collections…" value={collectionSearch} onChange={event => { setCollectionSearch(event.target.value); setSelected({}) }} className="max-w-56" />
          <select aria-label="Collection archive filter" className="rounded border border-border bg-background p-2 text-xs" value={collectionStatus} onChange={event => { setCollectionStatus(event.target.value as typeof collectionStatus); setSelected({}) }}>{['active', 'archived', 'all'].map(status => <option key={status} value={status}>{status} collections</option>)}</select>
          <Input aria-label="Search collection tasks" placeholder="Search tasks in each collection…" value={taskSearch} onChange={event => { setTaskSearch(event.target.value); setSelected({}) }} className="max-w-64" />
          <select aria-label="Sort collection tasks" className="rounded border border-border bg-background p-2 text-xs" value={taskSort} onChange={event => { setTaskSort(event.target.value); setSelected({}) }}>{['position', 'priority', 'status', 'updated_at', 'created_at'].map(sort => <option key={sort} value={sort}>{sort}</option>)}</select>
          <Button size="sm" variant="outline" onClick={() => { setTaskDir(previous => previous === 'asc' ? 'desc' : 'asc'); setSelected({}) }}>{taskDir === 'asc' ? 'Ascending' : 'Descending'}</Button>
          <Button size="sm" variant="ghost" onClick={() => { setTaskSearch(''); setTaskStatus(undefined); setTaskSort('position'); setTaskDir('asc'); setSelected({}) }}>Reset task filters</Button>
        </div>
        <div className="flex flex-wrap items-center gap-1 px-4 py-2" aria-label="Task status filters">
          <Button size="sm" variant={!taskStatus ? 'default' : 'outline'} aria-pressed={!taskStatus} onClick={() => { setTaskStatus(undefined); setSelected({}) }}>All statuses</Button>
          {TASK_STATUSES.map(status => <Button key={status} size="sm" variant={taskStatus === status ? 'default' : 'outline'} aria-pressed={taskStatus === status} onClick={() => { setTaskStatus(status); setSelected({}) }}>{STATUS_LABEL[status]}</Button>)}
        </div>
        <p className="px-4 text-xs text-muted-foreground">Search, status and order apply on the server to each task cohort. Selection covers only checked loaded rows. Drag ordering is available with unfiltered position order and a fully loaded destination.</p>
        {selectedIds.length > 0 && <div className="flex flex-wrap items-center gap-2 px-4 py-2">
          <span>{selectedIds.length} selected</span>
          <DropdownMenu><DropdownMenuTrigger render={<Button size="sm" variant="outline" />} disabled={actionBusy || stale}>Change selected status</DropdownMenuTrigger><DropdownMenuContent>{TASK_STATUSES.map(status => <DropdownMenuItem key={status} onClick={() => void applyAction('Change selected status', selectedIds, async id => { await api.transitionTask(id, status) })}>{STATUS_LABEL[status]}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>
          <Button size="sm" variant="outline" disabled={actionBusy || stale} onClick={() => setMoveIds(selectedIds)}>Move selected…</Button>
          <Button size="sm" variant="outline" disabled={actionBusy || stale || !selectedMembers.length} onClick={() => setRemoveTarget({ name: 'their collections', rows: selectedMembers })}>Remove {selectedMembers.length} collection assignments</Button>
          <Button size="sm" variant="ghost" disabled={actionBusy} onClick={() => setSelected({})}>Clear selection</Button>
        </div>}
        {actionResult && <div role={actionResult.failed.length ? 'alert' : 'status'} className="mx-4 my-2 rounded border border-border p-3 text-xs">
          {actionResult.label}: {actionResult.succeeded.length} {actionResult.label.startsWith('Remove') ? 'removed' : 'succeeded'}, {actionResult.failed.length} failed, {actionResult.notStarted.length} not attempted.
          {actionResult.failed.map(failure => <p key={failure.id}>{failure.id}: {failure.message}</p>)}
          {!!actionResult.notStarted.length && <p>Not attempted: {actionResult.notStarted.join(', ')}</p>}
        </div>}
        <ListPageControls page={{ ...collectionsPage, reload: load }} label="collections" />
        {stale && <p className="px-4 py-1 text-xs text-muted-foreground">Collections changed. Refresh to update the loaded pages.</p>}
        {loading && collections.length === 0 ? (
          <PageSkeleton />
        ) : error ? (
          <div className="m-6 rounded-md border border-rose-900/60 bg-rose-950/30 p-4 text-[13px] text-rose-300">
            {error.message}
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
                  {inboxTasks.length} loaded tasks
                </span>
              </div>
              <PagedCollectionTasks
                filters={filters}
                rowControls={rowControls}
                generation={generation}
                report={reportTasks}
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
                  busy={actionBusy}
                  onUnarchive={() => void applyAction('Unarchive collection', [collection.id], async id => { await api.unarchiveCollection(id) })}
                  clearDisabledReason={clearReason(collection.id)}
                  onClear={() => setRemoveTarget({ name: collection.name, rows: tasksByCollection[collection.id] ?? [], collectionId: collection.id, cohortKey: taskCohortKey })}
                />
                <PagedCollectionTasks
                  filters={filters}
                  rowControls={rowControls}
                  generation={generation}
                  report={reportTasks}
                  collectionKey={collection.id}
                  tasks={tasksByCollection[collection.id] ?? []}
                  emptyHint="Drop tasks here to add them to this collection."
                />
              </section>
            ))}

            {sortedCollections.length === 0 && (
              <div className="mx-4 my-6 rounded-md border border-dashed border-zinc-800 px-6 py-10 text-center text-[13px] text-zinc-500">
                No collections match this search and archive filter. Change the filters or create a collection.
              </div>
            )}
          </div>
        )}

        <CollectionCreateDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          onCreated={() => void load()}
        />

        {moveIds && <CollectionMoveDialog count={moveIds.length} busy={actionBusy} onClose={() => setMoveIds(null)} onChoose={id => { if (stale) { notifyError(new Error('Close this dialog and refresh changed pages before moving tasks.'), 'Pages changed'); return } void applyAction('Move tasks', moveIds, taskId => id ? api.moveTask(taskId, id) : api.addTaskToInbox(taskId)).then(() => setMoveIds(null)) }} />}
        <AlertDialog open={!!removeTarget} onOpenChange={open => { if (!open && !actionBusy) setRemoveTarget(null) }}>
          <AlertDialogContent><AlertDialogHeader><AlertDialogTitle>Remove {removeTarget?.rows.length ?? 0} tasks from "{removeTarget?.name}"?</AlertDialogTitle><AlertDialogDescription>This only removes them from the collection. Tasks are not deleted. Failed and unattempted IDs will be reported; the list refreshes from the server afterwards.</AlertDialogDescription></AlertDialogHeader>
            <AlertDialogFooter><AlertDialogCancel disabled={actionBusy}>Cancel</AlertDialogCancel><Button onClick={() => void confirmRemove()} disabled={actionBusy || stale || !!(removeTarget?.collectionId && (clearReason(removeTarget.collectionId) || removeTarget.cohortKey !== taskCohortKey))}>{actionBusy ? 'Removing…' : 'Remove assignments'}</Button></AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
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
