import { useRef, useState } from 'react'
import { Plus } from 'lucide-react'
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
  Input,
  Label,
} from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import type { Forum } from '@/lib/forums'

function suggestShortName(title: string) {
  return title
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 128)
}

export function ForumCreateDialog({
  disabled,
  onCreated,
}: {
  disabled?: boolean
  onCreated: (forum: Forum) => void
}) {
  const api = useApi()
  const [open, setOpen] = useState(false)
  const [title, setTitle] = useState('')
  const [customShortName, setCustomShortName] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const pending = useRef(false)
  const shortName = customShortName ?? suggestShortName(title)
  const shortNameError =
    shortName === 'topics' || shortName === 'posts'
      ? 'This short name is reserved. Please choose another.'
      : shortName && !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$/.test(shortName)
        ? 'Start with a letter or number, and use only letters, numbers, dashes or underscores (up to 128 characters).'
        : ''
  const valid = !!title.trim() && !!shortName && !shortNameError

  function changeOpen(next: boolean) {
    if (pending.current) return
    if (next) {
      setTitle('')
      setCustomShortName(null)
      setError('')
    }
    setOpen(next)
  }

  async function create() {
    if (!valid || pending.current) return
    pending.current = true
    setSubmitting(true)
    setError('')
    try {
      const { forum } = await api.forumCreate(shortName, title.trim())
      onCreated(forum)
      setOpen(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not create the forum. Please try again.')
    } finally {
      pending.current = false
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogTrigger disabled={disabled} render={<Button size="sm" />}>
        <Plus className="h-3.5 w-3.5" aria-hidden="true" />
        New Forum
      </DialogTrigger>
      <DialogContent
        className="rounded-none p-0"
        widthClassName="max-w-md"
        showCloseButton={!submitting}
      >
        <DialogHeader className="border-b border-zinc-800/80 px-4 py-4">
          <DialogTitle>New Forum</DialogTitle>
          <DialogDescription className="text-xs">
            Create a place for related topics and discussions.
          </DialogDescription>
        </DialogHeader>
        <form
          onSubmit={(event) => {
            event.preventDefault()
            void create()
          }}
          aria-busy={submitting}
        >
          <div className="space-y-4 px-4 py-4">
            <div className="space-y-1.5">
              <Label htmlFor="forum-name">Forum name</Label>
              <Input
                id="forum-name"
                className="rounded-none"
                placeholder="e.g. Engineering"
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                maxLength={300}
                required
                disabled={submitting}
                autoFocus
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="forum-short-name">Short name</Label>
              <Input
                id="forum-short-name"
                className="rounded-none font-mono"
                placeholder="e.g. engineering"
                value={shortName}
                onChange={(event) => setCustomShortName(event.target.value)}
                pattern="[a-zA-Z0-9][a-zA-Z0-9_\-]{0,127}"
                maxLength={128}
                required
                disabled={submitting}
                aria-invalid={!!shortNameError}
                aria-describedby={
                  shortNameError
                    ? 'forum-short-name-help forum-short-name-error'
                    : 'forum-short-name-help'
                }
              />
              <p id="forum-short-name-help" className="text-xs text-zinc-500">
                Used in the forum’s address. Choose a unique name using letters, numbers, dashes or
                underscores.
              </p>
              {shortNameError && (
                <p id="forum-short-name-error" className="text-xs text-red-300">
                  {shortNameError}
                </p>
              )}
            </div>
            {error && (
              <p role="alert" className="text-xs text-red-300">
                {error}
              </p>
            )}
          </div>
          <DialogFooter className="m-0 rounded-none border-zinc-800/80">
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={submitting}
              onClick={() => changeOpen(false)}
            >
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={submitting || !valid}>
              {submitting ? 'Creating…' : 'Create forum'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
