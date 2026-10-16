import { useEffect, useMemo, useState } from 'react'
import { useAgentsClient } from '../../agents/context'
import type { AgentRecord } from '../../agents/types'
import { useConsoleSeams } from '../../events/hooks'
import type { PendingPrSummary } from '../types'
import { assignAction } from './actions'
import {
  ASSIGN_TASKS,
  buildAssignmentPayload,
  renderAssignmentBody,
  suggestTask,
  type AssignmentRecord,
  type AssignTask,
} from './assign'

const field = 'w-full border border-zinc-700 bg-zinc-950 px-2 py-1.5 font-mono text-xs text-zinc-100 placeholder:text-zinc-600 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300'
const btn = 'border px-3 py-1.5 font-mono text-[11px] uppercase tracking-widest transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40'
const LIVE_DOT = { waiting: 'bg-amber-400', busy: 'bg-emerald-400', idle: 'bg-sky-400', offline: 'bg-zinc-600' } as const

/** Roles that fit the task best sort first. */
function fit(a: AgentRecord, task: AssignTask): number {
  const roles = a.identity.roles
  const want = task === 'review' ? ['reviewer'] : ['worker', 'implementer']
  return want.some((r) => roles.includes(r)) ? 0 : 1
}

export interface AssignDialogProps {
  pr: PendingPrSummary
  existing: AssignmentRecord[]
  onClose: () => void
  onDone?: (message: string) => void
}

/** Agent picker + task + note. Dispatches `pr.assign` through the ActionBus (IdentityConfirm runs there). */
export function AssignDialog({ pr, existing, onClose, onDone }: AssignDialogProps) {
  const agents = useAgentsClient()
  const { actionBus } = useConsoleSeams()
  const [list, setList] = useState<AgentRecord[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [task, setTask] = useState<AssignTask>(() => suggestTask(pr))
  const [agentId, setAgentId] = useState<string | null>(null)
  const [note, setNote] = useState('')
  const [setForge, setSetForge] = useState(true)
  const [preview, setPreview] = useState(false)
  const [search, setSearch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    agents.listAgents().then(
      (r) => live && setList(r.filter((a) => a.identity.enabled)),
      (e) => live && setLoadError(e instanceof Error ? e.message : 'Could not load agents'),
    )
    return () => {
      live = false
    }
  }, [agents])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && !busy && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose, busy])

  const shown = useMemo(() => {
    const q = search.trim().toLowerCase()
    return (list ?? [])
      .filter((a) => !q || `${a.identity.name} ${a.identity.id} ${a.identity.roles.join(' ')}`.toLowerCase().includes(q))
      .sort((a, b) => fit(a, task) - fit(b, task) || a.identity.name.localeCompare(b.identity.name))
  }, [list, search, task])

  const agent = list?.find((a) => a.identity.id === agentId) ?? null
  const dup = agent ? existing.find((r) => r.agentId === agent.identity.id && r.task === task) : undefined
  const body = useMemo(
    () => (agent ? renderAssignmentBody(buildAssignmentPayload(pr, task, agent.identity.id, { note })) : ''),
    [agent, pr, task, note],
  )

  const submit = async () => {
    if (!agent) return
    setBusy(true)
    setError(null)
    const res = await actionBus.dispatch(
      assignAction({ pr, agent: agent.identity, task, note, setForgeAssignee: setForge, force: !!dup }),
    )
    setBusy(false)
    if (res.status === 'ok') {
      onDone?.(res.message ?? 'Assigned')
      onClose()
    } else setError(res.status === 'denied' ? `Not confirmed: ${res.message ?? 'identity check failed'}` : (res.message ?? 'Assignment failed'))
  }

  const title = `${pr.ref.repo.owner}/${pr.ref.repo.name}#${pr.ref.number}`
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/60 p-4 pt-[8vh]" role="presentation" onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()}>
      <div role="dialog" aria-modal="true" aria-label={`Assign ${title} to an agent`} className="flex max-h-[84vh] w-full max-w-2xl flex-col border border-zinc-700 bg-zinc-950 shadow-2xl">
        <header className="border-b border-zinc-800 px-4 py-3">
          <p className="font-mono text-[10px] uppercase tracking-[0.2em] text-zinc-400">Assign to an agent</p>
          <p className="mt-1 truncate font-mono text-sm text-zinc-100">{title} <span className="text-zinc-400">{pr.title}</span></p>
        </header>

        <div className="space-y-4 overflow-y-auto px-4 py-4">
          <fieldset>
            <legend className="mb-1.5 font-mono text-[10px] uppercase tracking-widest text-zinc-500">Task</legend>
            <div className="flex flex-wrap gap-1.5">
              {ASSIGN_TASKS.map((t) => (
                <button key={t.id} type="button" aria-pressed={task === t.id} onClick={() => setTask(t.id)} className={`${btn} ${task === t.id ? 'border-amber-300 text-amber-300' : 'border-zinc-700 text-zinc-300 hover:bg-zinc-900'}`}>
                  {t.label}
                </button>
              ))}
            </div>
          </fieldset>

          <div>
            <div className="mb-1.5 flex items-center justify-between gap-3">
              <span className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">Agent</span>
              <input aria-label="Search agents" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search agents" className={`${field} max-w-[14rem]`} />
            </div>
            {loadError && <p role="alert" className="font-mono text-xs text-red-300">{loadError}</p>}
            {!list && !loadError && <p className="font-mono text-xs text-zinc-500">Loading agents…</p>}
            {list && shown.length === 0 && <p className="font-mono text-xs text-zinc-500">No matching enabled agents.</p>}
            <ul role="radiogroup" aria-label="Agents" className="max-h-52 overflow-y-auto border border-zinc-800">
              {shown.map((a) => {
                const on = a.identity.id === agentId
                const done = existing.some((r) => r.agentId === a.identity.id && r.task === task)
                return (
                  <li key={a.identity.id}>
                    <button type="button" role="radio" aria-checked={on} onClick={() => setAgentId(a.identity.id)} className={`flex w-full items-center gap-3 border-b border-zinc-800/80 px-3 py-2 text-left font-mono text-xs transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-amber-300 ${on ? 'bg-zinc-900 text-zinc-50' : 'text-zinc-200 hover:bg-zinc-900/50'}`}>
                      <span className={`h-1.5 w-1.5 shrink-0 ${LIVE_DOT[a.live?.state ?? 'offline']}`} aria-hidden />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate">{a.identity.name}</span>
                        <span className="block truncate text-[11px] text-zinc-400">{a.identity.roles.join(', ') || 'no role'} · {a.runtime.provider}{a.live ? ` · ${a.live.state}` : ' · not running'}</span>
                      </span>
                      {done && <span className="border border-emerald-500/50 px-1 text-[9px] uppercase tracking-widest text-emerald-300">assigned</span>}
                      {on && <span className="text-amber-300" aria-hidden>●</span>}
                    </button>
                  </li>
                )
              })}
            </ul>
            {agent && !agent.identity.urn && <p className="mt-1.5 font-mono text-[11px] text-amber-300">No registered address for this agent; the message goes to a fallback address and may not be delivered.</p>}
          </div>

          <label className="block">
            <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-zinc-500">Note for the agent (optional)</span>
            <textarea value={note} onChange={(e) => setNote(e.target.value)} rows={3} className={`${field} resize-y`} placeholder="Anything the PR context does not say" />
          </label>

          <label className="flex items-center gap-2 font-mono text-xs text-zinc-300">
            <input type="checkbox" checked={setForge} onChange={(e) => setSetForge(e.target.checked)} className="accent-amber-400" />
            Also set the agent as the PR assignee on the forge
          </label>

          {agent && (
            <div>
              <button type="button" onClick={() => setPreview((v) => !v)} aria-expanded={preview} className="font-mono text-[11px] uppercase tracking-widest text-zinc-400 hover:text-zinc-200">
                {preview ? '▾' : '▸'} Message preview
              </button>
              {preview && <pre className="mt-1.5 max-h-56 overflow-auto whitespace-pre-wrap border border-zinc-800 bg-zinc-900/60 p-2 font-mono text-[11px] text-zinc-300">{body}</pre>}
            </div>
          )}

          {dup && <p role="status" className="border border-amber-400/50 bg-amber-400/5 px-3 py-2 font-mono text-xs text-amber-200">Already assigned to {agent?.identity.name} for this task. Sending again repeats the message.</p>}
          {error && <p role="alert" className="border border-red-500/50 bg-red-500/5 px-3 py-2 font-mono text-xs text-red-300">{error}</p>}
        </div>

        <footer className="flex items-center justify-between gap-3 border-t border-zinc-800 px-4 py-3">
          <span className="font-mono text-[10px] text-zinc-500">Needs identity confirmation: {setForge ? 'forge assign + message' : 'message'}</span>
          <span className="flex gap-2">
            <button type="button" onClick={onClose} disabled={busy} className={`${btn} border-zinc-700 text-zinc-300 hover:bg-zinc-900`}>Cancel</button>
            <button type="button" onClick={submit} disabled={!agent || busy} className={`${btn} border-amber-300 text-amber-300 hover:bg-amber-300/10`}>
              {busy ? 'Assigning…' : dup ? 'Assign again' : 'Assign'}
            </button>
          </span>
        </footer>
      </div>
    </div>
  )
}
