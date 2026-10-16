// Pure inbox model: categorise, group, filter and sort pending PRs. No React.
import type { ItemState, ItemStatus } from '../../seams/state-store'
import { effectiveStatus } from '../../seams/state-store'
import { prKey, repoKey, type PendingPrSummary } from '../types'
import { assignmentsFor, type AssignmentRecord } from './assign'

export type InboxCategory = 'failing_checks' | 'changes_requested' | 'conflicts' | 'needs_review' | 'stale' | 'unassigned'

/** Display / grouping priority: a PR sits in the first category it matches. */
export const CATEGORY_ORDER: InboxCategory[] = ['failing_checks', 'changes_requested', 'conflicts', 'needs_review', 'stale', 'unassigned']

export const CATEGORY_LABEL: Record<InboxCategory, string> = {
  failing_checks: 'Failing checks',
  changes_requested: 'Changes requested',
  conflicts: 'Conflicts',
  needs_review: 'Needs review',
  stale: 'Stale',
  unassigned: 'Unassigned',
}

export const DEFAULT_STALE_MS = 7 * 86_400_000

/** Key under which triage (snooze / dismiss / settle) is stored in the StateStore. */
export const triageKey = (p: Pick<PendingPrSummary, 'ref'>): string => `pr-inbox:${prKey(p.ref)}`

export function categoriesOf(p: PendingPrSummary, now: number, staleMs = DEFAULT_STALE_MS): InboxCategory[] {
  const has = (r: string) => p.reasons.includes(r)
  const out = new Set<InboxCategory>()
  if (has('checks_failed')) out.add('failing_checks')
  if (has('changes_requested')) out.add('changes_requested')
  if (has('conflicting')) out.add('conflicts')
  if (has('review_requested')) out.add('needs_review')
  const updated = Date.parse(p.updatedAt)
  if (has('stale') || (Number.isFinite(updated) && now - updated >= staleMs)) out.add('stale')
  if (has('unassigned')) out.add('unassigned')
  return CATEGORY_ORDER.filter((c) => out.has(c))
}

export type TriageView = 'active' | 'snoozed' | 'dismissed' | 'settled' | 'all'

export interface InboxItem {
  pr: PendingPrSummary
  /** prKey, also the React key. */
  key: string
  triageKey: string
  categories: InboxCategory[]
  /** First of `categories`; the group the item sits in when grouped by category. */
  primary: InboxCategory | 'other'
  status: ItemStatus
  triage?: ItemState
  assignments: AssignmentRecord[]
}

export function buildItems(
  prs: PendingPrSummary[],
  state: Record<string, ItemState>,
  now: number,
  opts: { staleMs?: number } = {},
): InboxItem[] {
  return prs.map((pr) => {
    const categories = categoriesOf(pr, now, opts.staleMs)
    const key = prKey(pr.ref)
    const tk = triageKey(pr)
    const triage = state[tk]
    return {
      pr,
      key,
      triageKey: tk,
      categories,
      primary: categories[0] ?? 'other',
      status: effectiveStatus(triage, now),
      triage,
      assignments: assignmentsFor(key, state),
    }
  })
}

export interface InboxFilter {
  view: TriageView
  /** Empty = any. */
  categories: InboxCategory[]
  /** repoKey; empty = any. */
  repo: string
  search: string
  /** Only PRs with no assignment record and no forge assignee. */
  unassignedOnly: boolean
}

export const DEFAULT_FILTER: InboxFilter = { view: 'active', categories: [], repo: '', search: '', unassignedOnly: false }

const viewMatches = (view: TriageView, s: ItemStatus): boolean => {
  if (view === 'all') return true
  if (view === 'active') return s === 'unread' || s === 'read'
  return s === view
}

export function filterItems(items: InboxItem[], f: InboxFilter): InboxItem[] {
  const q = f.search.trim().toLowerCase()
  return items.filter((i) => {
    if (!viewMatches(f.view, i.status)) return false
    if (f.categories.length && !f.categories.some((c) => i.categories.includes(c))) return false
    if (f.repo && repoKey(i.pr.ref.repo) !== f.repo) return false
    if (f.unassignedOnly && (i.assignments.length > 0 || i.pr.assignees.length > 0)) return false
    if (q) {
      const hay = `${i.pr.title} ${i.pr.author.login} ${i.pr.ref.repo.owner}/${i.pr.ref.repo.name}#${i.pr.ref.number}`.toLowerCase()
      if (!hay.includes(q)) return false
    }
    return true
  })
}

export function countByView(items: InboxItem[]): Record<Exclude<TriageView, 'all'>, number> {
  const c = { active: 0, snoozed: 0, dismissed: 0, settled: 0 }
  for (const i of items) {
    if (i.status === 'unread' || i.status === 'read') c.active++
    else if (i.status === 'snoozed') c.snoozed++
    else if (i.status === 'dismissed') c.dismissed++
    else if (i.status === 'settled') c.settled++
  }
  return c
}

export function countByCategory(items: InboxItem[]): Record<InboxCategory, number> {
  const c = Object.fromEntries(CATEGORY_ORDER.map((k) => [k, 0])) as Record<InboxCategory, number>
  for (const i of items) for (const k of i.categories) c[k]++
  return c
}

export type GroupBy = 'repo' | 'category' | 'none'

export interface InboxGroup {
  id: string
  label: string
  items: InboxItem[]
}

const byRecent = (a: InboxItem, b: InboxItem) => b.pr.updatedAt.localeCompare(a.pr.updatedAt)

export function groupItems(items: InboxItem[], by: GroupBy): InboxGroup[] {
  if (by === 'none') return items.length ? [{ id: 'all', label: 'All pending', items: [...items].sort(byRecent) }] : []
  if (by === 'repo') {
    const m = new Map<string, InboxGroup>()
    for (const i of items) {
      const id = repoKey(i.pr.ref.repo)
      const g = m.get(id) ?? { id, label: `${i.pr.ref.repo.owner}/${i.pr.ref.repo.name}`, items: [] }
      g.items.push(i)
      m.set(id, g)
    }
    return [...m.values()].map((g) => ({ ...g, items: g.items.sort(byRecent) })).sort((a, b) => a.label.localeCompare(b.label))
  }
  const order = [...CATEGORY_ORDER, 'other' as const]
  return order
    .map((c) => ({ id: c, label: c === 'other' ? 'Other' : CATEGORY_LABEL[c], items: items.filter((i) => i.primary === c).sort(byRecent) }))
    .filter((g) => g.items.length)
}

export function repoOptions(items: InboxItem[]): { id: string; label: string }[] {
  const m = new Map<string, string>()
  for (const i of items) m.set(repoKey(i.pr.ref.repo), `${i.pr.ref.repo.owner}/${i.pr.ref.repo.name}`)
  return [...m].map(([id, label]) => ({ id, label })).sort((a, b) => a.label.localeCompare(b.label))
}

/** Snooze presets, relative to `now`. */
export const SNOOZE_PRESETS = [
  { id: '1h', label: '1 hour', ms: 3_600_000 },
  { id: '4h', label: '4 hours', ms: 4 * 3_600_000 },
  { id: '1d', label: 'Tomorrow', ms: 24 * 3_600_000 },
  { id: '1w', label: 'Next week', ms: 7 * 86_400_000 },
] as const
