import { useCallback, useEffect, useMemo, useState } from 'react'
import type { ActionBus } from '../../seams/action-bus'
import type { ForgeClient } from '../client'
import { prKey, type AddCommentInput, type Commit, type FileDiff, type ForgeComment, type ForgeUser, type PrRef, type PullRequest, type ReviewInput } from '../types'
import { commentAction, reviewAction } from './actions'

export interface PendingComment extends Omit<AddCommentInput, 'inReplyTo'> {
  id: string
}

export interface OptimisticComment {
  comment: ForgeComment
  state: 'posting'
}

export interface WorkspaceData {
  pr: PullRequest
  files: FileDiff[]
  commits: Commit[]
  comments: ForgeComment[]
}

export type WorkspaceLoad =
  | { status: 'loading' }
  | { status: 'error'; message: string; code?: string }
  | { status: 'ready'; data: WorkspaceData }

export interface WriteResult {
  ok: boolean
  message?: string
}

export interface Workspace {
  load: WorkspaceLoad
  reload(): void
  /** Optimistic placeholders currently being posted. */
  posting: ForgeComment[]
  /** Review comments queued for the next review submission. */
  pending: PendingComment[]
  queueComment(c: Omit<PendingComment, 'id'>): void
  unqueueComment(id: string): void
  postComment(input: AddCommentInput): Promise<WriteResult>
  submitReview(input: Omit<ReviewInput, 'comments'>): Promise<WriteResult>
}

let tmp = 0

/** Loads a PR (header, diff, comments) and owns optimistic writes through the ActionBus. */
export function useWorkspace(client: ForgeClient, bus: ActionBus, ref: PrRef, actor: ForgeUser): Workspace {
  const key = prKey(ref)
  const [state, setState] = useState<{ key: string; load: WorkspaceLoad }>({ key, load: { status: 'loading' } })
  const [version, setVersion] = useState(0)
  const [posting, setPosting] = useState<ForgeComment[]>([])
  const [pending, setPending] = useState<PendingComment[]>([])
  const { host, owner, name } = ref.repo
  const number = ref.number
  const stable = useMemo<PrRef>(() => ({ repo: { host, owner, name }, number }), [host, owner, name, number])

  useEffect(() => {
    let live = true
    Promise.all([client.getPr(stable), client.getDiff(stable), client.listCommits(stable), client.listComments(stable)]).then(
      ([pr, files, commits, comments]) => live && setState({ key, load: { status: 'ready', data: { pr, files, commits, comments } } }),
      (e: unknown) =>
        live && setState({ key, load: { status: 'error', message: e instanceof Error ? e.message : String(e), code: (e as { code?: string })?.code } }),
    )
    return () => {
      live = false
    }
  }, [client, key, version, stable])

  // Reset transient state when the PR changes.
  const [seenKey, setSeenKey] = useState(key)
  if (seenKey !== key) {
    setSeenKey(key)
    setPosting([])
    setPending([])
  }

  const load: WorkspaceLoad = state.key === key ? state.load : { status: 'loading' }

  const patch = useCallback((fn: (d: WorkspaceData) => WorkspaceData) => {
    setState((s) => (s.load.status === 'ready' ? { ...s, load: { status: 'ready', data: fn(s.load.data) } } : s))
  }, [])

  const postComment = useCallback(
    async (input: AddCommentInput): Promise<WriteResult> => {
      const id = `tmp-${++tmp}`
      const base = { id, author: actor, body: input.body, createdAt: new Date().toISOString() }
      const placeholder: ForgeComment = input.path
        ? { ...base, kind: 'review', path: input.path, line: input.line, side: input.side ?? 'right', inReplyTo: input.inReplyTo }
        : { ...base, kind: 'issue' }
      // The placeholder appears only once the step-up is granted; a declined step-up posts nothing.
      const res = await bus.dispatch(commentAction(stable, input, () => setPosting((p) => [...p, placeholder])))
      setPosting((p) => p.filter((c) => c.id !== id))
      if (res.status === 'ok') {
        patch((d) => ({ ...d, comments: [...d.comments, res.data as ForgeComment], pr: { ...d.pr, commentCount: (d.pr.commentCount ?? d.comments.length) + 1 } }))
        return { ok: true }
      }
      return { ok: false, message: res.message ?? `Could not post comment (${res.status})` }
    },
    [actor, bus, patch, stable],
  )

  const submitReview = useCallback(
    async (input: Omit<ReviewInput, 'comments'>): Promise<WriteResult> => {
      const comments = pending.map((c) => ({ body: c.body, path: c.path, line: c.line, side: c.side }))
      const res = await bus.dispatch(reviewAction(stable, { ...input, ...(comments.length ? { comments } : {}) }))
      if (res.status === 'ok') {
        setPending([])
        // Reviews add comments server-side; refetch for the true state.
        setVersion((v) => v + 1)
        return { ok: true }
      }
      return { ok: false, message: res.message ?? `Review not submitted (${res.status})` }
    },
    [bus, pending, stable],
  )

  return {
    load,
    reload: () => setVersion((v) => v + 1),
    posting,
    pending,
    queueComment: (c) => setPending((p) => [...p, { ...c, id: `q-${++tmp}` }]),
    unqueueComment: (id) => setPending((p) => p.filter((c) => c.id !== id)),
    postComment,
    submitReview,
  }
}
