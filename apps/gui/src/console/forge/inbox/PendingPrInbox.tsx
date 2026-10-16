import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ago } from '@/lib/agent-activity'
import { useMessaging } from '../../messaging/context'
import { useConsoleSeams } from '../../events/hooks'
import type { ForgeClient } from '../client'
import { getForgeClient } from '../refs/client'
import { repoKey } from '../types'
import { registerInboxActions, type InboxActionDeps } from './actions'
import { AssignDialog } from './AssignDialog'
import { useItemStates, useNow, usePendingPrs } from './hooks'
import { prWorkspaceHref } from './href'
import {
  buildItems,
  CATEGORY_LABEL,
  CATEGORY_ORDER,
  countByCategory,
  countByView,
  DEFAULT_FILTER,
  DEFAULT_STALE_MS,
  filterItems,
  groupItems,
  repoOptions,
  SNOOZE_PRESETS,
  type GroupBy,
  type InboxCategory,
  type InboxFilter,
  type InboxItem,
  type TriageView,
} from './model'
import { ASSIGN_TASKS } from './assign'
import { useAgentsClient } from '../../agents/context'

const field = 'border border-zinc-700 bg-zinc-950 px-2 py-1 font-mono text-xs text-zinc-100 placeholder:text-zinc-500 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300'
const btn = 'border px-2 py-1 font-mono text-[10px] uppercase tracking-widest transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40'
const ghost = `${btn} border-zinc-700 text-zinc-300 hover:bg-zinc-900`

const CATEGORY_TONE: Record<InboxCategory, string> = {
  failing_checks: 'border-red-500/60 text-red-300',
  changes_requested: 'border-orange-400/60 text-orange-300',
  conflicts: 'border-orange-400/60 text-orange-300',
  needs_review: 'border-sky-400/60 text-sky-300',
  stale: 'border-zinc-600 text-zinc-300',
  unassigned: 'border-zinc-600 text-zinc-400',
}

const VIEWS: { id: TriageView; label: string }[] = [
  { id: 'active', label: 'Active' },
  { id: 'snoozed', label: 'Snoozed' },
  { id: 'dismissed', label: 'Dismissed' },
  { id: 'settled', label: 'Resolved' },
]

function Pill({ category }: { category: InboxCategory }) {
  return <span className={`border px-1 font-mono text-[9px] uppercase tracking-widest ${CATEGORY_TONE[category]}`}>{CATEGORY_LABEL[category]}</span>
}

function Checks({ item }: { item: InboxItem }) {
  const c = item.pr.checksSummary
  const r = item.pr.reviewSummary
  if (!c && !r) return null
  return (
    <span className="font-mono text-[11px] text-zinc-400">
      {c && (
        <span title="checks">
          <span className="text-emerald-400">{c.passed}✓</span> <span className={c.failed ? 'text-red-400' : ''}>{c.failed}✗</span> <span>{c.pending}…</span>
        </span>
      )}
      {c && r && ' · '}
      {r && <span title="reviews">{r.approved} approved{r.changesRequested ? `, ${r.changesRequested} blocking` : ''}</span>}
    </span>
  )
}

interface RowProps {
  item: InboxItem
  now: number
  agentName: (id: string) => string
  onAssign: (i: InboxItem) => void
  onTriage: (i: InboxItem, op: 'dismiss' | 'settle' | 'reopen' | { snooze: number }) => Promise<void>
}

function Row({ item, now, agentName, onAssign, onTriage }: RowProps) {
  const { pr } = item
  const [menu, setMenu] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const active = item.status === 'unread' || item.status === 'read'
  const run = async (op: Parameters<RowProps['onTriage']>[1]) => {
    setBusy(true)
    setErr(null)
    try {
      await onTriage(item, op)
      setMenu(false)
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Could not save')
    } finally {
      setBusy(false)
    }
  }
  const label = `${pr.ref.repo.owner}/${pr.ref.repo.name}#${pr.ref.number}`
  return (
    <li className={`border-b border-zinc-800/80 px-4 py-3 ${active ? '' : 'opacity-70'}`} data-testid={`pr-row-${item.key}`}>
      <div className="flex flex-wrap items-start gap-x-4 gap-y-2">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <Link to={prWorkspaceHref(pr.ref)} className="font-mono text-sm text-zinc-100 underline-offset-2 hover:text-amber-300 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">
              {label}
            </Link>
            {item.categories.map((c) => <Pill key={c} category={c} />)}
            {item.status === 'snoozed' && item.triage?.snoozeUntil && <span className="font-mono text-[10px] text-zinc-400">snoozed until {new Date(item.triage.snoozeUntil).toLocaleString()}</span>}
          </div>
          <p className="mt-0.5 truncate text-sm text-zinc-300">{pr.title}</p>
          <p className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-[11px] text-zinc-400">
            <span>by {pr.author.login}</span>
            <span>updated {ago(Date.parse(pr.updatedAt), now)}</span>
            {pr.additions !== undefined && <span><span className="text-emerald-400">+{pr.additions}</span> <span className="text-red-400">-{pr.deletions ?? 0}</span></span>}
            <Checks item={item} />
          </p>
          {(pr.assignees.length > 0 || item.assignments.length > 0) && (
            <p className="mt-1 flex flex-wrap items-center gap-1.5 font-mono text-[11px] text-zinc-400">
              {pr.assignees.map((a) => <span key={a.login} className="border border-zinc-700 px-1">{a.login}</span>)}
              {item.assignments.map((a) => (
                <span key={a.key} className="border border-emerald-500/50 px-1 text-emerald-300" title={`message ${a.messageId ?? 'sent'} · ${ago(a.at, now)}`}>
                  {agentName(a.agentId)}: {ASSIGN_TASKS.find((t) => t.id === a.task)?.label.toLowerCase() ?? a.task} · sent
                </span>
              ))}
            </p>
          )}
        </div>

        <div className="relative flex flex-wrap items-center justify-end gap-1.5">
          <button type="button" className={`${btn} border-amber-300/70 text-amber-300 hover:bg-amber-300/10`} onClick={() => onAssign(item)} disabled={busy}>
            {item.assignments.length ? 'Assign again' : 'Assign'}
          </button>
          {active ? (
            <>
              <button type="button" className={ghost} aria-expanded={menu} onClick={() => setMenu((v) => !v)} disabled={busy}>Snooze</button>
              <button type="button" className={ghost} onClick={() => run('settle')} disabled={busy}>Resolve</button>
              <button type="button" className={ghost} onClick={() => run('dismiss')} disabled={busy}>Dismiss</button>
            </>
          ) : (
            <button type="button" className={ghost} onClick={() => run('reopen')} disabled={busy}>Reopen</button>
          )}
          {menu && (
            <ul role="menu" aria-label="Snooze for" className="absolute right-0 top-full z-10 mt-1 w-36 border border-zinc-700 bg-zinc-950 shadow-xl">
              {SNOOZE_PRESETS.map((p) => (
                <li key={p.id} role="none">
                  <button type="button" role="menuitem" className="w-full px-3 py-1.5 text-left font-mono text-xs text-zinc-200 hover:bg-zinc-900 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-amber-300" onClick={() => run({ snooze: now + p.ms })}>
                    {p.label}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
      {err && <p role="alert" className="mt-2 font-mono text-xs text-red-300">{err}</p>}
    </li>
  )
}

export interface PendingPrInboxProps {
  /** Defaults to the installed forge client (mock unless ?forge=http). */
  forge?: ForgeClient
  /** Poll interval for the list; 0 = load once + manual refresh. Default 60s. */
  pollMs?: number
  staleMs?: number
  initialFilter?: Partial<InboxFilter>
  initialGroupBy?: GroupBy
  /** Agent -> forge login override, see InboxActionDeps. */
  forgeLogin?: InboxActionDeps['forgeLogin']
}

/**
 * Pending-PR inbox. Mount under <ConsoleProvider> (StateStore + ActionBus) and a
 * router. Triage is stored in the StateStore, assignment goes through the bus.
 */
export function PendingPrInbox({ forge: forgeProp, pollMs = 60_000, staleMs = DEFAULT_STALE_MS, initialFilter, initialGroupBy = 'category', forgeLogin }: PendingPrInboxProps) {
  const forge = forgeProp ?? getForgeClient()
  const { actionBus, stateStore } = useConsoleSeams()
  const { client: messaging } = useMessaging()
  const agentsClient = useAgentsClient()
  const state = useItemStates()
  const now = useNow()
  const { prs, loading, error, refresh, refreshedAt } = usePendingPrs(forge, { pollMs })
  const [filter, setFilter] = useState<InboxFilter>({ ...DEFAULT_FILTER, ...initialFilter })
  const [groupBy, setGroupBy] = useState<GroupBy>(initialGroupBy)
  const [assigning, setAssigning] = useState<InboxItem | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [names, setNames] = useState<Record<string, string>>({})

  useEffect(() => registerInboxActions(actionBus, { forge, messaging, stateStore, forgeLogin }), [actionBus, forge, messaging, stateStore, forgeLogin])
  useEffect(() => {
    let live = true
    agentsClient.listAgents().then((a) => live && setNames(Object.fromEntries(a.map((x) => [x.identity.id, x.identity.name]))), () => undefined)
    return () => {
      live = false
    }
  }, [agentsClient])
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const all = useMemo(() => buildItems(prs, state, now, { staleMs }), [prs, state, now, staleMs])
  const views = countByView(all)
  // Category counts reflect the current triage view, so the chips match the list.
  const inView = useMemo(() => filterItems(all, { ...DEFAULT_FILTER, view: filter.view }), [all, filter.view])
  const cats = countByCategory(inView)
  const items = useMemo(() => filterItems(all, filter), [all, filter])
  const groups = useMemo(() => groupItems(items, groupBy), [items, groupBy])
  const repos = useMemo(() => repoOptions(all), [all])

  const triage: RowProps['onTriage'] = async (i, op) => {
    if (op === 'dismiss') await stateStore.apply({ type: 'dismiss', key: i.triageKey })
    else if (op === 'settle') await stateStore.apply({ type: 'settle', key: i.triageKey, resolution: 'resolved' })
    else if (op === 'reopen') await stateStore.apply({ type: 'reopen', key: i.triageKey })
    else await stateStore.apply({ type: 'snooze', key: i.triageKey, until: op.snooze })
  }
  const set = (p: Partial<InboxFilter>) => setFilter((f) => ({ ...f, ...p }))
  const toggleCat = (c: InboxCategory) => set({ categories: filter.categories.includes(c) ? filter.categories.filter((x) => x !== c) : [...filter.categories, c] })
  const filtered = filter.categories.length > 0 || !!filter.repo || !!filter.search || filter.unassignedOnly

  return (
    <section aria-label="Pending pull requests" className="border border-zinc-800 bg-zinc-950">
      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-zinc-800 px-4 py-3">
        <div>
          <h2 className="font-mono text-[11px] uppercase tracking-[0.2em] text-zinc-300">Pending PRs</h2>
          <p className="mt-0.5 font-mono text-[11px] text-zinc-500">
            {views.active} need attention{refreshedAt ? ` · refreshed ${ago(refreshedAt, now)}` : ''}
          </p>
        </div>
        <button type="button" className={ghost} onClick={() => void refresh()} disabled={loading}>{loading ? 'Loading…' : 'Refresh'}</button>
      </header>

      <div role="tablist" aria-label="Triage state" className="flex flex-wrap border-b border-zinc-800">
        {VIEWS.map((v) => (
          <button key={v.id} type="button" role="tab" aria-selected={filter.view === v.id} onClick={() => set({ view: v.id })} className={`border-r border-zinc-800 px-4 py-2 font-mono text-[11px] uppercase tracking-widest focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-amber-300 ${filter.view === v.id ? 'bg-zinc-900 text-amber-300' : 'text-zinc-400 hover:bg-zinc-900/50'}`}>
            {v.label} <span className="text-zinc-500">{views[v.id as keyof typeof views]}</span>
          </button>
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-2 border-b border-zinc-800 px-4 py-2">
        {CATEGORY_ORDER.map((c) => (
          <button key={c} type="button" aria-pressed={filter.categories.includes(c)} onClick={() => toggleCat(c)} className={`${btn} ${filter.categories.includes(c) ? 'border-amber-300 bg-amber-300/10 text-amber-300' : 'border-zinc-700 text-zinc-400 hover:bg-zinc-900'}`}>
            {CATEGORY_LABEL[c]} {cats[c]}
          </button>
        ))}
        <span className="mx-1 hidden h-4 w-px bg-zinc-800 sm:block" />
        <select aria-label="Repository" value={filter.repo} onChange={(e) => set({ repo: e.target.value })} className={field}>
          <option value="">All repos</option>
          {repos.map((r) => <option key={r.id} value={r.id}>{r.label}</option>)}
        </select>
        <select aria-label="Group by" value={groupBy} onChange={(e) => setGroupBy(e.target.value as GroupBy)} className={field}>
          <option value="category">Group: reason</option>
          <option value="repo">Group: repo</option>
          <option value="none">No grouping</option>
        </select>
        <input aria-label="Search pull requests" type="search" value={filter.search} onChange={(e) => set({ search: e.target.value })} placeholder="Search title, author, #" className={`${field} min-w-[10rem] flex-1`} />
        <label className="flex items-center gap-1.5 font-mono text-[11px] text-zinc-400">
          <input type="checkbox" checked={filter.unassignedOnly} onChange={(e) => set({ unassignedOnly: e.target.checked })} className="accent-amber-400" />
          Unassigned only
        </label>
        {filtered && <button type="button" className={ghost} onClick={() => setFilter((f) => ({ ...DEFAULT_FILTER, view: f.view }))}>Clear</button>}
      </div>

      {error && (
        <p role="alert" className="border-b border-red-500/40 bg-red-500/5 px-4 py-2 font-mono text-xs text-red-300">
          Could not load pending PRs: {error}
        </p>
      )}
      {toast && <p role="status" className="border-b border-emerald-500/40 bg-emerald-500/5 px-4 py-2 font-mono text-xs text-emerald-300">{toast}</p>}

      {loading && prs.length === 0 && !error ? (
        <p className="px-4 py-8 text-center font-mono text-xs text-zinc-500">Loading pending PRs…</p>
      ) : groups.length === 0 ? (
        <p className="px-4 py-8 text-center font-mono text-xs text-zinc-500">
          {all.length === 0 ? 'Nothing pending. All PRs are reviewed, green and assigned.' : filtered || filter.view !== 'active' ? 'No pull requests match this view.' : 'Inbox clear.'}
        </p>
      ) : (
        groups.map((g) => (
          <div key={g.id} role="group" aria-label={g.label}>
            {groupBy !== 'none' && (
              <h3 className="border-b border-zinc-800 bg-zinc-900/40 px-4 py-1.5 font-mono text-[10px] uppercase tracking-widest text-zinc-400">
                {g.label} <span className="text-zinc-500">{g.items.length}</span>
              </h3>
            )}
            <ul>
              {g.items.map((i) => (
                <Row key={`${repoKey(i.pr.ref.repo)}#${i.pr.ref.number}`} item={i} now={now} agentName={(id) => names[id] ?? id} onAssign={setAssigning} onTriage={triage} />
              ))}
            </ul>
          </div>
        ))
      )}

      {assigning && <AssignDialog pr={assigning.pr} existing={assigning.assignments} onClose={() => setAssigning(null)} onDone={(m) => { setToast(m); void refresh() }} />}
    </section>
  )
}
