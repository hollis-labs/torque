import type { RefEntity, StatusTone } from '../../refs'
import type { CheckRun, Commit, ForgeUser, PullRequest, Repo, RepoRef, Review } from '../types'
import { repoLabel, shortSha } from './parse'

const who = (u: ForgeUser): string => `@${u.login}${u.isAgent ? ' (agent)' : ''}`

export interface ChecksSummary {
  passed: number
  failed: number
  pending: number
  total: number
}

const FAILING = new Set(['failure', 'timed_out', 'action_required', 'cancelled'])

export function summarizeChecks(checks: readonly CheckRun[]): ChecksSummary {
  let passed = 0
  let failed = 0
  let pending = 0
  for (const c of checks) {
    if (c.status !== 'completed') pending++
    else if (c.conclusion && FAILING.has(c.conclusion)) failed++
    else passed++
  }
  return { passed, failed, pending, total: checks.length }
}

export function checksText(s: ChecksSummary): string {
  if (s.total === 0) return 'No checks'
  const parts = [`${s.passed}/${s.total} passed`]
  if (s.failed) parts.push(`${s.failed} failed`)
  if (s.pending) parts.push(`${s.pending} running`)
  return parts.join(', ')
}

export function checksTone(s: ChecksSummary): StatusTone {
  return s.failed ? 'danger' : s.pending ? 'warning' : s.total ? 'success' : 'neutral'
}

/** Latest verdict per reviewer, ignoring plain comments and dismissed reviews. */
export function reviewSummary(reviews: readonly Review[]): { approved: number; changes: number } {
  const latest = new Map<string, Review['state']>()
  for (const r of reviews) {
    if (r.state === 'approved' || r.state === 'changes_requested') latest.set(r.author.login, r.state)
    else if (r.state === 'dismissed') latest.delete(r.author.login)
  }
  let approved = 0
  let changes = 0
  for (const s of latest.values()) {
    if (s === 'approved') approved++
    else changes++
  }
  return { approved, changes }
}

export function reviewText(reviews: readonly Review[]): string {
  const s = reviewSummary(reviews)
  if (s.changes) return `${s.changes} requested changes${s.approved ? `, ${s.approved} approved` : ''}`
  if (s.approved) return `${s.approved} approved`
  return reviews.length ? 'Commented only' : 'No reviews'
}

export function sizeText(pr: Pick<PullRequest, 'additions' | 'deletions' | 'changedFiles'>): string | undefined {
  if (pr.additions === undefined && pr.deletions === undefined && pr.changedFiles === undefined) return undefined
  const files = pr.changedFiles !== undefined ? ` in ${pr.changedFiles} file${pr.changedFiles === 1 ? '' : 's'}` : ''
  return `+${pr.additions ?? 0} −${pr.deletions ?? 0}${files}`
}

export function prStatus(pr: Pick<PullRequest, 'state' | 'draft'>): { label: string; tone: StatusTone } {
  if (pr.state === 'merged') return { label: 'merged', tone: 'info' }
  if (pr.state === 'closed') return { label: 'closed', tone: 'danger' }
  if (pr.draft || pr.state === 'draft') return { label: 'draft', tone: 'neutral' }
  return { label: 'open', tone: 'success' }
}

/** The hovercard data for a PR: title, state, author, checks, review state, size. */
export function prEntity(pr: PullRequest): RefEntity {
  const checks = summarizeChecks(pr.checks)
  const fields: NonNullable<RefEntity['fields']> = [
    { label: 'Author', value: who(pr.author) },
    { label: 'Branch', value: `${pr.headBranch} → ${pr.baseBranch}`, mono: true },
    { label: 'Checks', value: checksText(checks) },
    { label: 'Review', value: reviewText(pr.reviews) },
  ]
  const size = sizeText(pr)
  if (size) fields.push({ label: 'Size', value: size, mono: true })
  if (pr.state === 'open' && pr.mergeable === 'conflicting') fields.push({ label: 'Merge', value: 'Has conflicts' })
  return {
    title: pr.title,
    subtitle: `${repoLabel(pr.ref.repo)}#${pr.ref.number}`,
    description: pr.body,
    status: prStatus(pr),
    fields,
    updatedAt: pr.mergedAt ?? pr.updatedAt,
  }
}

export interface CommitContext {
  repo: RepoRef
  commit: Commit
  /** The PR the commit was found in (the contract has no direct commit lookup yet). */
  pr?: PullRequest
}

export function commitEntity({ repo, commit, pr }: CommitContext): RefEntity {
  const [first, ...rest] = commit.message.split('\n')
  const fields: NonNullable<RefEntity['fields']> = [
    { label: 'Author', value: who(commit.author) },
    { label: 'SHA', value: commit.sha, mono: true },
  ]
  if (pr) fields.push({ label: 'In PR', value: `#${pr.ref.number}${sizeText(pr) ? ` (${sizeText(pr)})` : ''}`, mono: true })
  return {
    title: first || shortSha(commit.sha),
    subtitle: `${repoLabel(repo)}@${shortSha(commit.sha)}`,
    description: rest.join('\n').trim() || undefined,
    fields,
    updatedAt: commit.authoredAt,
  }
}

export function repoEntity(repo: Repo): RefEntity {
  const fields: NonNullable<RefEntity['fields']> = [{ label: 'Default branch', value: repo.defaultBranch, mono: true }]
  if (repo.openIssues !== undefined) fields.push({ label: 'Open issues', value: String(repo.openIssues) })
  if (repo.stars !== undefined) fields.push({ label: 'Stars', value: String(repo.stars) })
  return {
    title: repoLabel(repo),
    subtitle: repo.description,
    status: repo.private === undefined ? undefined : { label: repo.private ? 'private' : 'public', tone: 'neutral' },
    fields,
    updatedAt: repo.updatedAt,
  }
}
