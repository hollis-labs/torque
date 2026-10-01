import { useEffect, useState } from 'react'
import { Inbox, Plus } from 'lucide-react'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@hollis-labs/sysop-ui'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@hollis-labs/sysop-ui'
import { Skeleton } from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { usePagedList } from '@/hooks/use-paged-list'
import { useListSearch } from '@/hooks/use-list-search'
import { ListPageControls } from '@/components/domain/list-page-controls'
import { notifyError } from '@/lib/toast'
import type { Collection, Task, TaskSummary } from '@/lib/types'

interface QuickAddDialogProps {
  task: TaskSummary
  open: boolean
  onOpenChange: (open: boolean) => void
  /**
   * Called after a successful assignment. Receives the updated task so
   * callers can patch local state. Optional — many call sites are happy
   * to let the dialog close as confirmation.
   */
  onAssigned?: (task: Task) => void
}

const RECENT_LIMIT = 8

/**
 * Quick-add command palette for assigning a single task to a collection.
 *
 * Empty input shows recent active collections (most-recently-updated
 * first) plus a pinned "Send to inbox" option. Typing filters by
 * server-side text search; when a complete result page has no exact name match, a "Create new
 * collection" option appears at the bottom of the list.
 *
 * Selecting a collection routes through addTaskToCollection if the task
 * is fresh, or moveTask if it already lives somewhere — the backend
 * rejects re-adds of a task that's already a member, so the move path is
 * the canonical way to switch collections.
 */
export function QuickAddDialog({
  task,
  open,
  onOpenChange,
  onAssigned,
}: QuickAddDialogProps) {
  const api = useApi()
  const [query, setQuery] = useState('')
  const [current, setCurrent] = useState<{ id: string; name: string } | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const trimmedQuery = query.trim()
  const search = useListSearch(query)
  const page = usePagedList({
    enabled: open,
    params: { search, sort_by: search ? 'name' : 'updated_at', sort_dir: search ? 'asc' as const : 'desc' as const, limit: search ? 50 : RECENT_LIMIT },
    fetchPage: ({ params, cursor, signal }) => api.listCollections('active', { ...params, cursor }, signal),
    getId: collection => collection.id,
  })
  // Excluding the current assignment is a no-op guard, not search over a local catalog.
  const visibleCollections = page.items.filter(collection => collection.id !== task.collection_id)
  const exactMatch = page.items.some(collection => collection.name.toLowerCase() === trimmedQuery.toLowerCase())
  const showCreate = trimmedQuery.length > 0 && search === trimmedQuery && !page.loading && !page.hasMore && !page.error && !exactMatch
  const currentName = current && current.id === task.collection_id ? current.name : null
  useEffect(() => {
    if (!open || !task.collection_id) return
    let canceled = false
    api.getCollection(task.collection_id).then(collection => {
      if (!canceled) setCurrent({ id: collection.id, name: collection.name })
    }).catch(() => {})
    return () => { canceled = true }
  }, [api, open, task.collection_id])

  const headerText = (() => {
    if (currentName) return `Currently in: ${currentName}`
    if (!task.collection_id && task.added_to_collections_at) {
      return 'Currently in inbox'
    }
    return null
  })()

  async function applyResult(updatedTaskId: string) {
    // Always refetch — addTaskToCollection / moveTask / addTaskToInbox
    // all return 204, so we need the GET to surface the new collection
    // metadata to the caller.
    try {
      const fresh = await api.getTask(updatedTaskId)
      onAssigned?.(fresh)
    } catch {
      // Non-fatal: caller's own onAssigned is best-effort. The backend
      // mutation already succeeded at this point.
    }
  }

  async function handleSelectCollection(collection: Collection) {
    if (submitting) return
    setSubmitting(true)
    try {
      if (task.collection_id) {
        await api.moveTask(task.id, collection.id)
      } else {
        await api.addTaskToCollection(collection.id, task.id)
      }
      await applyResult(task.id)
      onOpenChange(false)
    } catch (err) {
      notifyError(err, `Failed to add to ${collection.name}`)
    } finally {
      setSubmitting(false)
    }
  }

  async function handleSendToInbox() {
    if (submitting) return
    setSubmitting(true)
    try {
      await api.addTaskToInbox(task.id)
      await applyResult(task.id)
      onOpenChange(false)
    } catch (err) {
      notifyError(err, 'Failed to send to inbox')
    } finally {
      setSubmitting(false)
    }
  }

  async function handleCreateAndAdd() {
    if (submitting || !trimmedQuery) return
    setSubmitting(true)
    try {
      const created = await api.createCollection(trimmedQuery)
      await api.addTaskToCollection(created.id, task.id)
      await applyResult(task.id)
      onOpenChange(false)
    } catch (err) {
      notifyError(err, 'Failed to create collection')
    } finally {
      setSubmitting(false)
    }
  }

  const isLoading = page.loading && !page.items.length
  // Always pin "Send to inbox" at the top — cmdk auto-highlights the first
  // item, which makes Enter-without-picking land the task in inbox per spec.
  // Hide only when the task already lives in inbox (no-op + clutter).
  const isCurrentlyInInbox =
    !task.collection_id && task.added_to_collections_at != null
  const showInbox = !isCurrentlyInInbox

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) setQuery(''); onOpenChange(next) }}>
      <DialogContent className="overflow-hidden p-0 sm:max-w-lg">
        <DialogHeader className="sr-only">
          <DialogTitle>Add to collection</DialogTitle>
          <DialogDescription>
            Search for a collection or create a new one. Press Enter to assign.
          </DialogDescription>
        </DialogHeader>

        {headerText && (
          <div className="border-b border-zinc-800/80 px-4 py-2 text-[11px] uppercase tracking-[.18em] text-zinc-400">
            {headerText}
          </div>
        )}

        <Command shouldFilter={false} className="rounded-none">
          <CommandInput
            placeholder="Search collections or type a new name…"
            value={query}
            onValueChange={setQuery}
            disabled={submitting}
          />
          <ListPageControls page={page} label="collections" />
          <CommandList className="max-h-80">
            {isLoading ? (
              <div className="space-y-2 p-3">
                <Skeleton className="h-7 w-full rounded-md" />
                <Skeleton className="h-7 w-full rounded-md" />
                <Skeleton className="h-7 w-2/3 rounded-md" />
              </div>
            ) : (
              <>
                {showInbox && (
                  <>
                    <CommandGroup heading="Quick">
                      <CommandItem
                        value="__inbox__"
                        onSelect={handleSendToInbox}
                        disabled={submitting}
                      >
                        <Inbox className="size-4" aria-hidden />
                        Send to inbox
                      </CommandItem>
                    </CommandGroup>
                    {(visibleCollections.length > 0 || showCreate) && (
                      <CommandSeparator />
                    )}
                  </>
                )}

                {visibleCollections.length > 0 && (
                  <CommandGroup
                    heading={trimmedQuery ? 'Collections' : 'Recent collections'}
                  >
                    {visibleCollections.map((collection) => (
                      <CommandItem
                        key={collection.id}
                        value={`collection:${collection.id}`}
                        onSelect={() => handleSelectCollection(collection)}
                        disabled={submitting}
                      >
                        <span className="truncate">{collection.name}</span>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                )}

                {showCreate && (
                  <>
                    {visibleCollections.length > 0 && <CommandSeparator />}
                    <CommandGroup heading="Create">
                      <CommandItem
                        value="__create__"
                        onSelect={handleCreateAndAdd}
                        disabled={submitting}
                      >
                        <Plus className="size-4" aria-hidden />
                        <span>
                          Create new collection:{' '}
                          <span className="font-medium text-zinc-100">
                            "{trimmedQuery}"
                          </span>
                        </span>
                      </CommandItem>
                    </CommandGroup>
                  </>
                )}

                {!showInbox &&
                  visibleCollections.length === 0 &&
                  !showCreate && (
                    <CommandEmpty>No collections found.</CommandEmpty>
                  )}
              </>
            )}
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  )
}
