import { useEffect, useState } from 'react'
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Input,
} from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { allForumPages } from '@/lib/forums'
import type { Forum, ForumParticipant } from '@/lib/forums'
import { forumFieldClass } from './forum-management'

export function ForumAccess({ forum, onChanged }: { forum: Forum; onChanged: () => void }) {
  const api = useApi()
  const [open, setOpen] = useState(false)
  const [participants, setParticipants] = useState<ForumParticipant[]>([])
  const [attempt, setAttempt] = useState(0)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [kind, setKind] = useState('agent')
  const [id, setID] = useState('')
  const [access, setAccess] = useState('post')
  const [ownerKind, setOwnerKind] = useState('operator')
  const [ownerID, setOwnerID] = useState('')
  const [confirmOwner, setConfirmOwner] = useState(false)
  useEffect(() => {
    if (!open) return
    const c = new AbortController()
    allForumPages((opts, signal) => api.forumParticipants(forum.id, opts, signal), {}, c.signal)
      .then((out) => {
        if (!c.signal.aborted) {
          setParticipants(out)
          setError('')
        }
      })
      .catch((e) => {
        if (!c.signal.aborted) setError(e instanceof Error ? e.message : 'Could not load access.')
      })
      .finally(() => {
        if (!c.signal.aborted) setLoading(false)
      })
    return () => c.abort()
  }, [api, open, forum.id, attempt])
  async function mutate(run: () => Promise<unknown>) {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await run()
      setLoading(true)
      setAttempt((n) => n + 1)
      onChanged()
      setConfirmOwner(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not update access.')
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Button
        size="sm"
        variant="ghost"
        onClick={() => {
          setLoading(true)
          setOpen(true)
        }}
      >
        Access
      </Button>
      <Dialog
        open={open}
        onOpenChange={(v) => {
          if (!busy) setOpen(v)
        }}
      >
        <DialogContent className="rounded-none" widthClassName="max-w-xl">
          <DialogHeader>
            <DialogTitle>Access to {forum.title}</DialogTitle>
            <DialogDescription>
              Grant read or post access to a stable user or agent principal.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 text-xs">
            <p className="text-zinc-400">
              Owner: {forum.owner_type} {forum.owner_id}. Owners and the operator retain implicit
              access.
            </p>
            {loading && <p role="status">Loading participants…</p>}
            {participants.map((p) => (
              <div
                key={`${p.kind}:${p.id}`}
                className="flex items-center gap-3 border-b border-zinc-800 py-2"
              >
                <span className="min-w-0 flex-1 break-all">
                  {p.kind} · {p.id} · {p.access}
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy}
                  onClick={() => void mutate(() => api.forumRevoke(forum.id, p.kind, p.id))}
                >
                  Revoke {p.id}
                </Button>
              </div>
            ))}
            <form
              className="space-y-2"
              onSubmit={(e) => {
                e.preventDefault()
                void mutate(() =>
                  api.forumGrant(forum.id, {
                    principal_kind: kind,
                    principal_id: id.trim(),
                    access,
                  }),
                )
              }}
            >
              <fieldset disabled={busy} className="space-y-2">
                <label className="block">
                  Principal kind
                  <select
                    aria-label="Participant kind"
                    className={forumFieldClass}
                    value={kind}
                    onChange={(e) => setKind(e.target.value)}
                  >
                    <option value="agent">Agent</option>
                    <option value="user">User</option>
                  </select>
                </label>
                <label className="block">
                  Principal ID
                  <Input
                    aria-label="Participant ID"
                    required
                    className={forumFieldClass}
                    value={id}
                    onChange={(e) => setID(e.target.value)}
                    placeholder="Stable agent URN or user ID"
                  />
                </label>
                <label className="block">
                  Access
                  <select
                    aria-label="Participant access"
                    className={forumFieldClass}
                    value={access}
                    onChange={(e) => setAccess(e.target.value)}
                  >
                    <option value="read">Read</option>
                    <option value="post">Read and post</option>
                  </select>
                </label>
                <Button type="submit" size="sm" disabled={!id.trim()}>
                  Grant / update access
                </Button>
              </fieldset>
            </form>
            <details>
              <summary className="cursor-pointer">Transfer ownership</summary>
              <form
                className="mt-3 space-y-3"
                onSubmit={(e) => {
                  e.preventDefault()
                  void mutate(() =>
                    api.forumSetOwner(
                      forum.id,
                      ownerKind === 'operator'
                        ? {}
                        : { owner_type: ownerKind, owner_id: ownerID.trim() },
                    ),
                  )
                }}
              >
                <fieldset disabled={busy} className="space-y-2">
                  <label className="block">
                    New owner
                    <select
                      aria-label="Owner kind"
                      className={forumFieldClass}
                      value={ownerKind}
                      onChange={(e) => {
                        setOwnerKind(e.target.value)
                        setConfirmOwner(false)
                      }}
                    >
                      <option value="operator">Current operator</option>
                      <option value="user">User</option>
                      <option value="agent">Agent</option>
                    </select>
                  </label>
                  {ownerKind !== 'operator' && (
                    <Input
                      aria-label="Owner ID"
                      className={forumFieldClass}
                      required
                      value={ownerID}
                      onChange={(e) => {
                        setOwnerID(e.target.value)
                        setConfirmOwner(false)
                      }}
                    />
                  )}
                  <p className="text-zinc-400">
                    Explicit grants are preserved. Grant access to the previous owner first if they
                    should retain access.
                  </p>
                  <label className="flex gap-2">
                    <input
                      type="checkbox"
                      required
                      checked={confirmOwner}
                      onChange={(e) => setConfirmOwner(e.target.checked)}
                    />
                    Confirm ownership transfer
                  </label>
                  <Button
                    type="submit"
                    size="sm"
                    disabled={!confirmOwner || (ownerKind !== 'operator' && !ownerID.trim())}
                  >
                    Transfer ownership
                  </Button>
                </fieldset>
              </form>
            </details>
            {error && (
              <p role="alert" className="text-red-300">
                {error}{' '}
                <Button
                  variant="ghost"
                  onClick={() => {
                    setLoading(true)
                    setAttempt((n) => n + 1)
                  }}
                >
                  Retry loading
                </Button>
              </p>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}
