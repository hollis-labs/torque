import { useEffect, useMemo, useRef, useState } from 'react'
import { Inbox, Plus } from 'lucide-react'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { useApi } from '@/hooks/use-api'
import { notifyError } from '@/lib/toast'
import type { Collection, Task } from '@/lib/types'

interface QuickAddDialogProps {
  task: Task
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
 * case-insensitive substring; if no collection matches, a "Create new
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
  const [collections, setCollections] = useState<Collection[] | null>(null)
  const [currentName, setCurrentName] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  // Track the in-flight load so a fast re-open doesn't race with the
  // previous fetch and overwrite a fresher result with a stale one.
  const loadTokenRef = useRef(0)

  // Re-fetch on open. Empty out stale state on close so the next open
  // doesn't briefly flash the previous task's "Currently in" header.
  useEffect(() => {
    if (!open) {
      setQuery('')
      setCollections(null)
      setCurrentName(null)
      return
    }

    const token = ++loadTokenRef.current
    setCollections(null)
    setCurrentName(null)

    api
      .listCollections('active')
      .then((list) => {
        if (loadTokenRef.current !== token) return
        setCollections(list)
        if (task.collection_id) {
          // Prefer the cached list — avoids an extra round-trip in the
          // common case. Falls back to a direct fetch if (somehow) the
          // task's collection isn't active anymore.
          const hit = list.find((c) => c.id === task.collection_id)
          if (hit) {
            setCurrentName(hit.name)
          } else {
            api
              .getCollection(task.collection_id)
              .then((c) => {
                if (loadTokenRef.current !== token) return
                setCurrentName(c.name)
              })
              .catch(() => {
                // Non-fatal — header just won't render.
              })
          }
        }
      })
      .catch((err) => {
        if (loadTokenRef.current !== token) return
        notifyError(err, 'Failed to load collections')
        setCollections([])
      })
  }, [open, api, task.collection_id])

  const trimmedQuery = query.trim()
  const lowerQuery = trimmedQuery.toLowerCase()

  // When the input is empty, surface the most-recently-updated active
  // collections; when there's a query, filter the full list by case-
  // insensitive substring. Both paths exclude the task's current
  // collection so the user isn't offered a no-op assignment.
  const visibleCollections = useMemo(() => {
    if (!collections) return []
    const filtered = trimmedQuery
      ? collections.filter((c) => c.name.toLowerCase().includes(lowerQuery))
      : [...collections]
          .sort((a, b) => (b.updated_at ?? '').localeCompare(a.updated_at ?? ''))
          .slice(0, RECENT_LIMIT)
    return filtered.filter((c) => c.id !== task.collection_id)
  }, [collections, trimmedQuery, lowerQuery, task.collection_id])

  // Show "Create new" only when there's a query AND no exact-name match
  // among ALL collections (active + filtered out). Prevents the user from
  // accidentally creating a duplicate of an existing-but-filtered name.
  const exactMatch = useMemo(() => {
    if (!trimmedQuery || !collections) return false
    return collections.some((c) => c.name.toLowerCase() === lowerQuery)
  }, [trimmedQuery, lowerQuery, collections])
  const showCreate = trimmedQuery.length > 0 && !exactMatch

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

  const isLoading = collections === null
  // Always pin "Send to inbox" at the top — cmdk auto-highlights the first
  // item, which makes Enter-without-picking land the task in inbox per spec.
  // Hide only when the task already lives in inbox (no-op + clutter).
  const isCurrentlyInInbox =
    !task.collection_id && task.added_to_collections_at != null
  const showInbox = !isCurrentlyInInbox

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
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
