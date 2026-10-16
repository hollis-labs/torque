import { useEffect, useState, type ReactNode } from 'react'
import { ago } from '@/lib/agent-activity'
import type { RefDetailProps } from '../../autolink/extensions'
import { RefChip } from '../../autolink/RefChip'
import { TONE } from '../../autolink/tone'
import type { StatusTone } from '../../refs'
import type { ForgeClient } from '../client'
import type { CheckRun, Commit, FileDiff, PullRequest, Repo, Review } from '../types'
import { getForgeClient } from './client'
import { findCommit } from './resolvers'
import { checksText, checksTone, reviewText, sizeText, summarizeChecks } from './entity'
import { PrWorkspaceLink } from '../workspace/PrWorkspaceLink'
import { parseCommitCanonical, parsePrCanonical, parseRepoCanonical, prCanonical, shortSha } from './parse'

// Read-only drawer bodies. Anything that writes (comment, review, assign) is
// G3/G4 and must go through ActionBus + IdentityConfirm, never from here.

const LABEL = 'font-mono text-[10px] uppercase tracking-widest text-zinc-500'

type Async<T> = { status: 'loading' } | { status: 'ready'; data: T } | { status: 'error'; message: string }

function useForgeData<T>(key: string, load: (c: ForgeClient) => Promise<T>): Async<T> {
  const [state, setState] = useState<{ key: string; value: Async<T> }>({ key, value: { status: 'loading' } })
  useEffect(() => {
    let live = true
    load(getForgeClient()).then(
      (data) => live && setState({ key, value: { status: 'ready', data } }),
      (e: unknown) => live && setState({ key, value: { status: 'error', message: e instanceof Error ? e.message : String(e) } }),
    )
    return () => {
      live = false
    }
    // `load` is a fresh closure per render; the key identifies the request.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  return state.key === key ? state.value : { status: 'loading' }
}

function Section({ title, count, children }: { title: string; count?: number; children: ReactNode }) {
  return (
    <section className="border-t border-zinc-800/80 py-3 first:border-t-0 first:pt-0">
      <h3 className={`${LABEL} mb-2`}>
        {title}
        {count !== undefined && <span className="ml-1.5 text-zinc-500">{count}</span>}
      </h3>
      {children}
    </section>
  )
}

function Pill({ tone, children }: { tone: StatusTone; children: ReactNode }) {
  return <span className={`inline-flex shrink-0 items-center border px-1.5 py-px font-mono text-[10px] uppercase tracking-wider ${TONE[tone]}`}>{children}</span>
}

function Loading() {
  return (
    <div className="space-y-1.5" aria-hidden>
      <div className="h-2 w-2/3 animate-pulse bg-zinc-800" />
      <div className="h-2 w-1/3 animate-pulse bg-zinc-800" />
    </div>
  )
}

function Failed({ message }: { message: string }) {
  return <p className="text-[12px] text-red-300" role="alert">Could not load: {message}</p>
}

function Empty({ children }: { children: ReactNode }) {
  return <p className="text-[12px] text-zinc-500">{children}</p>
}

function Rows({ children }: { children: ReactNode }) {
  return <ul className="divide-y divide-zinc-800/60 border-y border-zinc-800/60">{children}</ul>
}

function checkTone(c: CheckRun): StatusTone {
  if (c.status !== 'completed') return 'warning'
  if (c.conclusion === 'success') return 'success'
  if (c.conclusion === 'neutral' || c.conclusion === 'skipped') return 'neutral'
  return 'danger'
}
const checkLabel = (c: CheckRun): string => (c.status !== 'completed' ? c.status.replace('_', ' ') : (c.conclusion ?? 'done').replace('_', ' '))

function reviewTone(r: Review): StatusTone {
  return r.state === 'approved' ? 'success' : r.state === 'changes_requested' ? 'danger' : 'neutral'
}

function useNow(): number {
  const [now] = useState(() => Date.now())
  return now
}

function Who({ login, agent }: { login: string; agent?: boolean }) {
  return <span className="font-mono text-zinc-300">@{login}{agent ? ' · agent' : ''}</span>
}

function PrBody({ pr }: { pr: PullRequest }) {
  const checks = summarizeChecks(pr.checks)
  return (
    <>
      <Section title="Checks">
        <div className="mb-2 flex items-center gap-2">
          <Pill tone={checksTone(checks)}>{checksText(checks)}</Pill>
        </div>
        {pr.checks.length > 0 && (
          <Rows>
            {pr.checks.map((c) => (
              <li key={c.id} className="flex items-center gap-2 py-1.5 text-[12px]">
                <span className="min-w-0 flex-1 truncate text-zinc-200">{c.name}</span>
                <Pill tone={checkTone(c)}>{checkLabel(c)}</Pill>
              </li>
            ))}
          </Rows>
        )}
      </Section>
      <Section title="Reviews" count={pr.reviews.length}>
        {pr.reviews.length === 0 ? (
          <Empty>No reviews yet. {pr.reviewers?.length ? `Requested from ${pr.reviewers.map((r) => `@${r.login}`).join(', ')}.` : ''}</Empty>
        ) : (
          <>
            <p className="mb-2 text-[12px] text-zinc-400">{reviewText(pr.reviews)}</p>
            <Rows>
              {pr.reviews.map((r) => (
                <li key={r.id} className="flex items-center gap-2 py-1.5 text-[12px]">
                  <span className="min-w-0 flex-1 truncate"><Who login={r.author.login} agent={r.author.isAgent} /></span>
                  <Pill tone={reviewTone(r)}>{r.state.replace('_', ' ')}</Pill>
                </li>
              ))}
            </Rows>
          </>
        )}
      </Section>
    </>
  )
}

export function ForgePrDetail({ refId }: RefDetailProps) {
  const now = useNow()
  const ref = parsePrCanonical(refId.canonical)
  const pr = useForgeData(refId.canonical, async (c) => {
    if (!ref) throw new Error('malformed pull request reference')
    const [p, commits, files] = await Promise.all([c.getPr(ref), c.listCommits(ref), c.getDiff(ref)])
    return { p, commits, files }
  })
  if (pr.status === 'loading') return <Loading />
  if (pr.status === 'error') return <Failed message={pr.message} />
  const { p, commits, files } = pr.data
  return (
    <div data-testid="forge-pr-detail">
      {ref && (
        <PrWorkspaceLink
          prRef={ref}
          className="mb-3 inline-flex h-8 items-center border border-amber-300 bg-amber-300 px-3 font-mono text-[11px] uppercase tracking-widest text-zinc-950 hover:bg-amber-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300"
        >
          Open workspace
        </PrWorkspaceLink>
      )}
      <Section title="Summary">
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-[12px]">
          <dt className={LABEL}>Author</dt>
          <dd><Who login={p.author.login} agent={p.author.isAgent} /></dd>
          {p.assignees.length > 0 && (
            <>
              <dt className={LABEL}>Assignees</dt>
              <dd className="text-zinc-200">{p.assignees.map((a) => `@${a.login}`).join(', ')}</dd>
            </>
          )}
          <dt className={LABEL}>Branch</dt>
          <dd className="font-mono text-zinc-200 [overflow-wrap:anywhere]">{p.headBranch} {'→'} {p.baseBranch}</dd>
          <dt className={LABEL}>Merge</dt>
          <dd className="text-zinc-200">{p.state === 'merged' ? `Merged ${p.mergedAt ? ago(Date.parse(p.mergedAt), now) : ''}` : p.mergeable}</dd>
          {sizeText(p) && (
            <>
              <dt className={LABEL}>Size</dt>
              <dd className="font-mono text-zinc-200">{sizeText(p)}</dd>
            </>
          )}
          {p.labels && p.labels.length > 0 && (
            <>
              <dt className={LABEL}>Labels</dt>
              <dd className="flex flex-wrap gap-1">{p.labels.map((l) => <Pill key={l} tone="neutral">{l}</Pill>)}</dd>
            </>
          )}
        </dl>
      </Section>
      <PrBody pr={p} />
      <Section title="Commits" count={commits.length}>
        {commits.length === 0 ? <Empty>No commits.</Empty> : (
          <Rows>
            {commits.map((c: Commit) => (
              <li key={c.sha} className="flex items-baseline gap-2 py-1.5 text-[12px]">
                <RefChip kind="forge.commit" canonical={`${refId.canonical.replace(/#\d+$/, '')}@${c.sha}`} raw={shortSha(c.sha)} hovercard={false} resolve="hover" />
                <span className="min-w-0 flex-1 truncate text-zinc-300">{c.message.split('\n')[0]}</span>
              </li>
            ))}
          </Rows>
        )}
      </Section>
      <Section title="Files" count={files.length}>
        {files.length === 0 ? <Empty>No file changes.</Empty> : (
          <Rows>
            {files.map((f: FileDiff) => (
              <li key={f.path} className="flex items-center gap-2 py-1.5 font-mono text-[11px]">
                <span className="min-w-0 flex-1 truncate text-zinc-300" title={f.path}>{f.path}</span>
                <span className="shrink-0 text-emerald-400">+{f.additions}</span>
                <span className="shrink-0 text-red-400">{'−'}{f.deletions}</span>
              </li>
            ))}
          </Rows>
        )}
      </Section>
    </div>
  )
}

export function ForgeCommitDetail({ refId }: RefDetailProps) {
  const now = useNow()
  const parsed = parseCommitCanonical(refId.canonical)
  const res = useForgeData(refId.canonical, async (c) => {
    if (!parsed) throw new Error('malformed commit reference')
    const hit = await findCommit(c, parsed.repo, parsed.sha)
    if (!hit) throw new Error('commit not found in any pull request')
    return hit
  })
  if (res.status === 'loading') return <Loading />
  if (res.status === 'error') return <Failed message={res.message} />
  const { commit, pr } = res.data
  const body = commit.message.split('\n').slice(1).join('\n').trim()
  return (
    <div data-testid="forge-commit-detail">
      <Section title="Commit">
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-[12px]">
          <dt className={LABEL}>Author</dt>
          <dd><Who login={commit.author.login} agent={commit.author.isAgent} /></dd>
          <dt className={LABEL}>Authored</dt>
          <dd className="text-zinc-200">{ago(Date.parse(commit.authoredAt), now)}</dd>
          <dt className={LABEL}>SHA</dt>
          <dd className="font-mono text-zinc-200 [overflow-wrap:anywhere]">{commit.sha}</dd>
          {commit.parents && commit.parents.length > 0 && (
            <>
              <dt className={LABEL}>Parents</dt>
              <dd className="font-mono text-zinc-200">{commit.parents.map(shortSha).join(', ')}</dd>
            </>
          )}
        </dl>
        {body && <p className="mt-2 whitespace-pre-wrap text-[12px] leading-relaxed text-zinc-300 [overflow-wrap:anywhere]">{body}</p>}
      </Section>
      <Section title="Pull request">
        <RefChip kind="forge.pr" canonical={prCanonical(pr.ref)} hovercard={false} />
        <p className="mt-2 text-[12px] text-zinc-400">{pr.title}</p>
      </Section>
    </div>
  )
}

export function ForgeRepoDetail({ refId }: RefDetailProps) {
  const now = useNow()
  const ref = parseRepoCanonical(refId.canonical)
  const res = useForgeData(refId.canonical, async (c) => {
    if (!ref) throw new Error('malformed repository reference')
    const [repo, prs] = await Promise.all([c.getRepo(ref), c.listPrs({ repo: ref, state: 'open', limit: 8 })])
    return { repo: repo as Repo, prs: prs.items }
  })
  if (res.status === 'loading') return <Loading />
  if (res.status === 'error') return <Failed message={res.message} />
  const { repo, prs } = res.data
  return (
    <div data-testid="forge-repo-detail">
      <Section title="Repository">
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-[12px]">
          <dt className={LABEL}>Default branch</dt>
          <dd className="font-mono text-zinc-200">{repo.defaultBranch}</dd>
          {repo.updatedAt && (
            <>
              <dt className={LABEL}>Pushed</dt>
              <dd className="text-zinc-200">{ago(Date.parse(repo.updatedAt), now)}</dd>
            </>
          )}
        </dl>
      </Section>
      <Section title="Open pull requests" count={prs.length}>
        {prs.length === 0 ? <Empty>No open pull requests.</Empty> : (
          <Rows>
            {prs.map((p) => (
              <li key={p.ref.number} className="flex items-baseline gap-2 py-1.5 text-[12px]">
                <RefChip kind="forge.pr" canonical={prCanonical(p.ref)} hovercard={false} resolve="hover" />
                <span className="min-w-0 flex-1 truncate text-zinc-300">{p.title}</span>
              </li>
            ))}
          </Rows>
        )}
      </Section>
    </div>
  )
}
