import { useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ExternalLink, GitPullRequest, RefreshCw } from 'lucide-react'
import { ago } from '@/lib/agent-activity'
import { SeamsContext } from '../../events/context'
import { Btn } from '../../agents/ui'
import { StepUpHost } from '../../agents/StepUpDialog'
import { Linkified } from '../../autolink/Linkified'
import { TONE } from '../../autolink/tone'
import type { StatusTone } from '../../refs'
import { createActionBus, MockIdentityConfirm, type ActionBus, type IdentityConfirm } from '../../seams'
import type { ForgeClient } from '../client'
import { checksText, checksTone, reviewText, sizeText, summarizeChecks } from '../refs/entity'
import { getForgeClient } from '../refs/client'
import { hostLabel, prUrl } from '../refs/parse'
import type { CheckRun, ForgeUser, PrRef, PullRequest, Review } from '../types'
import { registerWorkspaceActions } from './actions'
import { Author, Badge, CommentItem, Composer, LABEL, ThreadView } from './Comments'
import { buildThreadIndex } from './diff-utils'
import { FileDiffView, type DiffMode } from './DiffView'
import { FileTree } from './FileTree'
import { ReviewPanel } from './ReviewPanel'
import { diffLineCount, fileAnchorId, LARGE_FILE_LINES } from './diff-utils'
import { useWorkspace } from './useWorkspace'

export interface PrWorkspaceProps {
  prRef: PrRef
  /** Defaults to the installed ForgeClient (mock; `?forge=http` selects HTTP). */
  client?: ForgeClient
  bus?: ActionBus
  identity?: IdentityConfirm
  /** Hide every write affordance. */
  readOnly?: boolean
  /** File to scroll to once loaded. */
  initialFile?: string
  initialMode?: DiffMode
  className?: string
}

type Tab = 'files' | 'conversation' | 'checks'

const ACTOR: ForgeUser = { login: 'you' }

function Pill({ tone, children }: { tone: StatusTone; children: ReactNode }) {
  return <span className={`inline-flex shrink-0 items-center border px-1.5 py-px font-mono text-[10px] uppercase tracking-wider ${TONE[tone]}`}>{children}</span>
}

function stateTone(pr: PullRequest): StatusTone {
  if (pr.state === 'merged') return 'info'
  if (pr.state === 'closed') return 'danger'
  return pr.draft ? 'neutral' : 'success'
}

function checkTone(c: CheckRun): StatusTone {
  if (c.status !== 'completed') return 'warning'
  if (c.conclusion === 'success') return 'success'
  if (c.conclusion === 'neutral' || c.conclusion === 'skipped') return 'neutral'
  return 'danger'
}

function mergeNote(pr: PullRequest): { tone: StatusTone; text: string } {
  if (pr.state === 'merged') return { tone: 'info', text: pr.mergedAt ? `Merged ${new Date(pr.mergedAt).toISOString().slice(0, 10)}` : 'Merged' }
  if (pr.state === 'closed') return { tone: 'danger', text: 'Closed without merging' }
  const checks = summarizeChecks(pr.checks)
  const changes = pr.reviews.some((r) => r.state === 'changes_requested')
  if (pr.mergeable === 'conflicting') return { tone: 'danger', text: 'Has merge conflicts' }
  if (pr.draft) return { tone: 'neutral', text: 'Draft: not ready to merge' }
  if (checks.failed) return { tone: 'danger', text: 'Checks failing' }
  if (changes) return { tone: 'danger', text: 'Changes requested' }
  if (checks.pending) return { tone: 'warning', text: 'Waiting on checks' }
  if (pr.mergeable === 'blocked') return { tone: 'warning', text: 'Blocked by branch rules' }
  if (pr.mergeable === 'unknown') return { tone: 'neutral', text: 'Merge state not computed yet' }
  return { tone: 'success', text: 'Ready to merge' }
}

function useSeams(bus?: ActionBus, identity?: IdentityConfirm): { bus: ActionBus; identity: IdentityConfirm } {
  const seams = useContext(SeamsContext)
  return useMemo(() => {
    if (bus && identity) return { bus, identity }
    if (seams) return { bus: bus ?? seams.actionBus, identity: identity ?? seams.identity }
    // No Console provider: a local bus with a MANUAL mock step-up, so the confirm dialog always shows.
    const id = identity ?? new MockIdentityConfirm({ mode: 'manual' })
    return { bus: bus ?? createActionBus({ identity: id }), identity: id }
  }, [bus, identity, seams])
}

/**
 * PR workspace: header (state, checks, reviews, merge state), file tree,
 * unified/split diff with inline threads, conversation, checks, and review
 * submission. Reads and writes go through the ForgeClient; writes dispatch on
 * the ActionBus (reviews behind IdentityConfirm).
 */
export function PrWorkspace({ prRef, client: clientProp, bus: busProp, identity: identityProp, readOnly: readOnlyProp, initialFile, initialMode = 'unified', className }: PrWorkspaceProps) {
  const client = useMemo(() => clientProp ?? getForgeClient(), [clientProp])
  const { bus, identity } = useSeams(busProp, identityProp)
  useEffect(() => registerWorkspaceActions(bus, client), [bus, client])
  const ws = useWorkspace(client, bus, prRef, ACTOR)

  const [tab, setTab] = useState<Tab>('files')
  const [mode, setMode] = useState<DiffMode>(initialMode)
  const [reviewing, setReviewing] = useState(false)
  const [closedFiles, setClosedFiles] = useState<Set<string> | null>(null)
  const [openedLarge, setOpenedLarge] = useState<Set<string>>(new Set())
  const [active, setActive] = useState<string | undefined>(initialFile)
  const [now] = useState(() => Date.now())

  const ready = ws.load.status === 'ready' ? ws.load.data : null
  const index = useMemo(() => (ready ? buildThreadIndex(ready.comments, ready.files, ready.pr.headSha) : null), [ready])
  const threadsByPath = useMemo(() => {
    const m = new Map<string, number>()
    for (const t of index?.all ?? []) m.set(t.path, (m.get(t.path) ?? 0) + 1)
    return m
  }, [index])

  const isLarge = (path: string) => {
    const f = ready?.files.find((x) => x.path === path)
    return !!f && diffLineCount(f) >= LARGE_FILE_LINES
  }
  const isOpen = (path: string) => (isLarge(path) ? openedLarge.has(path) || path === initialFile : !(closedFiles?.has(path) ?? false))
  const toggle = (path: string) => {
    if (isLarge(path)) setOpenedLarge((s) => toggled(s, path))
    else setClosedFiles((s) => toggled(s ?? new Set(), path))
  }

  const select = (path: string) => {
    setActive(path)
    setTab('files')
    if (isLarge(path)) setOpenedLarge((s) => new Set(s).add(path))
    else setClosedFiles((s) => (s?.has(path) ? toggled(s, path) : s))
    requestAnimationFrame(() => document.getElementById(fileAnchorId(path))?.scrollIntoView?.({ block: 'start' }))
  }

  const loaded = ready !== null
  useEffect(() => {
    if (!initialFile || !loaded) return
    const id = requestAnimationFrame(() => document.getElementById(fileAnchorId(initialFile))?.scrollIntoView?.({ block: 'start' }))
    return () => cancelAnimationFrame(id)
  }, [initialFile, loaded])

  if (ws.load.status === 'loading') {
    return (
      <div className={`p-6 ${className ?? ''}`} role="status" aria-label="Loading pull request" data-testid="pr-workspace-loading">
        <div className="space-y-2" aria-hidden>
          <div className="h-3 w-1/3 animate-pulse bg-zinc-800" />
          <div className="h-2 w-1/4 animate-pulse bg-zinc-800" />
          <div className="mt-6 h-40 animate-pulse bg-zinc-900" />
        </div>
      </div>
    )
  }
  if (ws.load.status === 'error' || !ready || !index) {
    const err = ws.load.status === 'error' ? ws.load : { message: 'Unknown error', code: undefined }
    return (
      <div className={`p-6 ${className ?? ''}`} role="alert" data-testid="pr-workspace-error">
        <h1 className="font-mono text-sm uppercase tracking-widest text-red-300">{err.code === 'not_found' ? 'Pull request not found' : 'Could not load pull request'}</h1>
        <p className="mt-2 text-[13px] text-zinc-400">{err.message}</p>
        <Btn className="mt-4" onClick={ws.reload}>Retry</Btn>
      </div>
    )
  }

  const { pr, files, commits } = ready
  const readOnly = !!readOnlyProp || pr.state === 'merged' || pr.state === 'closed'
  const checks = summarizeChecks(pr.checks)
  const merge = mergeNote(pr)
  const href = pr.url ?? prUrl(prRef)
  const convo = timeline(ready.comments.filter((c) => c.kind === 'issue'), pr.reviews)
  const postingIssue = ws.posting.filter((p) => p.kind === 'issue')
  const allOpen = files.every((f) => isOpen(f.path) || isLarge(f.path))

  return (
    <div className={`flex h-full min-h-[480px] flex-col bg-bg ${className ?? ''}`} data-testid="pr-workspace">
      <header className="shrink-0 border-b border-zinc-800/80 px-4 pb-0 pt-3">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <GitPullRequest className="h-4 w-4 shrink-0 text-zinc-500" aria-hidden />
          <h1 className="min-w-0 text-[15px] font-medium text-zinc-100 [overflow-wrap:anywhere]">
            {pr.title} <span className="font-mono text-zinc-500">#{pr.ref.number}</span>
          </h1>
          <Pill tone={stateTone(pr)}>{pr.draft && pr.state === 'open' ? 'draft' : pr.state}</Pill>
          <span className="ml-auto flex items-center gap-2">
            <Btn onClick={ws.reload} aria-label="Refresh pull request"><RefreshCw className="inline h-3 w-3" /></Btn>
            {href && (
              <a href={href} target="_blank" rel="noreferrer" className="inline-flex h-8 items-center gap-1.5 border border-zinc-700 px-3 font-mono text-[11px] uppercase tracking-widest text-zinc-200 hover:bg-zinc-900 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">
                {hostLabel(prRef.repo.host)} <ExternalLink className="h-3 w-3" />
              </a>
            )}
            {!readOnly && (
              <Btn tone="primary" onClick={() => setReviewing((r) => !r)} aria-expanded={reviewing}>
                Review{ws.pending.length ? ` (${ws.pending.length})` : ''}
              </Btn>
            )}
          </span>
        </div>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[12px] text-zinc-400">
          <span className="font-mono text-zinc-500">{prRef.repo.owner}/{prRef.repo.name}</span>
          <Author user={pr.author} />
          <span className="font-mono text-zinc-300 [overflow-wrap:anywhere]">{pr.headBranch} {'→'} {pr.baseBranch}</span>
          {sizeText(pr) && <span className="font-mono">{sizeText(pr)}</span>}
          <Pill tone={merge.tone}>{merge.text}</Pill>
          <Pill tone={checksTone(checks)}>{checksText(checks)}</Pill>
          <span className="text-zinc-400">{reviewText(pr.reviews)}</span>
        </div>
        <nav className="mt-2 flex gap-0 font-mono text-[11px] uppercase tracking-widest" aria-label="Workspace sections" role="tablist">
          {([
            ['files', `Files changed ${files.length}`],
            ['conversation', `Conversation ${ready.comments.length}`],
            ['checks', `Checks ${pr.checks.length}`],
          ] as [Tab, string][]).map(([id, label]) => (
            <button
              key={id}
              role="tab"
              type="button"
              aria-selected={tab === id}
              onClick={() => setTab(id)}
              className={`-mb-px border-b-2 px-3 py-2 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-amber-300 ${tab === id ? 'border-amber-300 text-zinc-100' : 'border-transparent text-zinc-500 hover:text-zinc-300'}`}
            >
              {label}
            </button>
          ))}
        </nav>
      </header>

      {reviewing && !readOnly && (
        <ReviewPanel pending={ws.pending} onUnqueue={ws.unqueueComment} onClose={() => setReviewing(false)} onSubmit={({ verdict, body }) => ws.submitReview({ verdict, body })} />
      )}

      {tab === 'files' && (
        <div className="flex min-h-0 flex-1">
          <aside className="hidden w-72 shrink-0 flex-col border-r border-zinc-800/80 md:flex">
            <FileTree files={files} active={active} threads={threadsByPath} onSelect={select} />
          </aside>
          <div className="min-w-0 flex-1 overflow-auto" data-testid="forge-diff-scroll">
            <div className="flex flex-wrap items-center gap-3 border-b border-zinc-800/80 px-3 py-2">
              <div className="flex" role="group" aria-label="Diff layout">
                {(['unified', 'split'] as DiffMode[]).map((m) => (
                  <button
                    key={m}
                    type="button"
                    aria-pressed={mode === m}
                    onClick={() => setMode(m)}
                    className={`h-7 border px-3 font-mono text-[11px] uppercase tracking-widest focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 ${mode === m ? 'border-amber-300 bg-amber-300/10 text-amber-200' : 'border-zinc-700 text-zinc-400 hover:text-zinc-200'}`}
                  >
                    {m}
                  </button>
                ))}
              </div>
              <Btn className="!h-7" onClick={() => (allOpen ? setClosedFiles(new Set(files.map((f) => f.path))) : setClosedFiles(new Set()))}>
                {allOpen ? 'Collapse all' : 'Expand all'}
              </Btn>
              <span className="font-mono text-[11px] text-zinc-500">
                {files.length} file{files.length === 1 ? '' : 's'} · <span className="text-emerald-400">+{files.reduce((n, f) => n + f.additions, 0)}</span>{' '}
                <span className="text-red-400">{'−'}{files.reduce((n, f) => n + f.deletions, 0)}</span>
              </span>
              {readOnly && <Badge>read only</Badge>}
            </div>
            {files.length === 0 && <p className="px-4 py-6 text-[13px] text-zinc-500">No file changes in this pull request.</p>}
            {files.map((f) => (
              <FileDiffView
                key={f.path}
                file={f}
                mode={mode}
                open={isOpen(f.path)}
                onToggle={() => toggle(f.path)}
                index={index}
                posting={ws.posting}
                pending={ws.pending.filter((p) => p.path === f.path)}
                readOnly={readOnly}
                forgeUrl={href}
                postComment={ws.postComment}
                queueComment={ws.queueComment}
                unqueueComment={ws.unqueueComment}
              />
            ))}
          </div>
        </div>
      )}

      {tab === 'conversation' && (
        <div className="min-h-0 flex-1 overflow-auto" data-testid="forge-conversation">
          <div className="max-w-4xl">
            <Section title="Description">
              <div className="px-4 py-3 text-[13px] leading-relaxed text-zinc-300 [overflow-wrap:anywhere]">
                {pr.body?.trim() ? <Linkified text={pr.body} preserveWhitespace /> : <span className="text-zinc-500">No description provided.</span>}
              </div>
              {commits.length > 0 && <p className="border-t border-zinc-800/60 px-4 py-2 font-mono text-[11px] text-zinc-500">{commits.length} commit{commits.length === 1 ? '' : 's'}, latest {ago(Date.parse(commits[commits.length - 1].authoredAt), now)}</p>}
            </Section>
            <Section title={`Timeline ${convo.length + postingIssue.length}`}>
              <div className="divide-y divide-zinc-800/60">
                {convo.length + postingIssue.length === 0 && <p className="px-4 py-3 text-[12px] text-zinc-500">No comments yet.</p>}
                {convo.map((e) =>
                  e.type === 'comment' ? (
                    <CommentItem key={e.comment.id} c={e.comment} />
                  ) : (
                    <div key={e.review.id} className="px-3 py-2" data-testid="forge-review-entry">
                      <div className="flex flex-wrap items-center gap-2">
                        <Author user={e.review.author} />
                        <Pill tone={reviewTone(e.review)}>{e.review.state.replace('_', ' ')}</Pill>
                      </div>
                      {e.review.body && <div className="mt-1 text-[13px] text-zinc-300 [overflow-wrap:anywhere]"><Linkified text={e.review.body} preserveWhitespace /></div>}
                    </div>
                  ),
                )}
                {postingIssue.map((c) => <CommentItem key={c.id} c={c} posting />)}
              </div>
            </Section>
            {index.orphaned.length > 0 && (
              <Section title={`Comments on files no longer in the diff ${index.orphaned.length}`}>
                {index.orphaned.map((t) => (
                  <ThreadView key={t.id} thread={t} posting={ws.posting} readOnly={readOnly} showPath onReply={(id, body) => ws.postComment({ body, path: t.path, line: t.root.line, side: t.root.side, inReplyTo: id })} />
                ))}
              </Section>
            )}
            {!readOnly && (
              <Section title="Leave a comment">
                <div className="px-4 py-3">
                  <Composer placeholder="Add a comment…" submitLabel="Comment" onSubmit={(body) => ws.postComment({ body })} />
                </div>
              </Section>
            )}
          </div>
        </div>
      )}

      {tab === 'checks' && (
        <div className="min-h-0 flex-1 overflow-auto" data-testid="forge-checks">
          <div className="max-w-4xl">
            <Section title={`Checks ${pr.checks.length}`}>
              {pr.checks.length === 0 ? (
                <p className="px-4 py-3 text-[12px] text-zinc-500">No checks reported for this pull request.</p>
              ) : (
                <ul className="divide-y divide-zinc-800/60">
                  {pr.checks.map((c) => (
                    <li key={c.id} className="flex items-center gap-3 px-4 py-2 text-[13px]">
                      <span className="min-w-0 flex-1 truncate text-zinc-200">{c.name}</span>
                      <Pill tone={checkTone(c)}>{c.status !== 'completed' ? c.status.replace('_', ' ') : (c.conclusion ?? 'done').replace('_', ' ')}</Pill>
                      {c.url && <a href={c.url} target="_blank" rel="noreferrer" className="text-zinc-500 hover:text-zinc-200" aria-label={`Open ${c.name} on the forge`}><ExternalLink className="h-3.5 w-3.5" /></a>}
                    </li>
                  ))}
                </ul>
              )}
            </Section>
            <Section title="Merge state">
              <div className="space-y-1 px-4 py-3 text-[13px] text-zinc-300">
                <Pill tone={merge.tone}>{merge.text}</Pill>
                <p className="text-[12px] text-zinc-500">Merging is done on the forge for now; the Console has no merge operation yet.</p>
              </div>
            </Section>
          </div>
        </div>
      )}
      <StepUpHost identity={identity} />
    </div>
  )
}

function toggled<T>(s: Set<T>, v: T): Set<T> {
  const n = new Set(s)
  if (!n.delete(v)) n.add(v)
  return n
}

function reviewTone(r: Review): StatusTone {
  return r.state === 'approved' ? 'success' : r.state === 'changes_requested' ? 'danger' : 'neutral'
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="border-b border-zinc-800/80">
      <h2 className={`${LABEL} border-b border-zinc-800/60 px-4 py-2`}>{title}</h2>
      {children}
    </section>
  )
}

type TimelineEntry = { type: 'comment'; at: string; comment: import('../types').ForgeComment } | { type: 'review'; at: string; review: Review }

function timeline(issue: import('../types').ForgeComment[], reviews: Review[]): TimelineEntry[] {
  return [
    ...issue.map((comment): TimelineEntry => ({ type: 'comment', at: comment.createdAt, comment })),
    ...reviews.filter((r) => r.state !== 'pending').map((review): TimelineEntry => ({ type: 'review', at: review.submittedAt ?? '', review })),
  ].sort((a, b) => a.at.localeCompare(b.at))
}
