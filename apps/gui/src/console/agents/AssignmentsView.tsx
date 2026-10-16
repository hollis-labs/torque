import { useCallback, useEffect, useMemo, useState } from 'react'
import { EmptyState } from '@hollis-labs/sysop-ui'
import { ago } from '@/lib/agent-activity'
import { useAgentsClient } from './context'
import { Field, inputCls } from './AgentEditor'
import type { AssignmentDraft, AssignmentRecord, AssignmentTargetType } from './lifecycle'
import type { AgentPlan } from './plan'
import { PlanRunner } from './PlanRunner'
import { Block, Btn, Chip } from './ui'
import type { AgentRecord } from './types'

type Group = 'agent' | 'target'

/** Agent <-> workstream/task assignments. Changes are staged, then reviewed as one plan. */
export function AssignmentsView({ agents, now: nowProp }: { agents: AgentRecord[]; now?: number }) {
  const client = useAgentsClient()
  const [rows, setRows] = useState<AssignmentRecord[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [group, setGroup] = useState<Group>('agent')
  const [addList, setAddList] = useState<AssignmentDraft[]>([])
  const [removeIds, setRemoveIds] = useState<string[]>([])
  const [form, setForm] = useState({ agentId: '', type: 'task' as AssignmentTargetType, targetId: '', label: '', role: '' })
  const [note, setNote] = useState('')
  const [plan, setPlan] = useState<AgentPlan | null>(null)
  const [busy, setBusy] = useState(false)
  const [now] = useState(() => nowProp ?? Date.now())

  const load = useCallback(() => client.listAssignments().then((r) => (setRows(r), setError(null)), (e) => setError(e instanceof Error ? e.message : 'Assignments unavailable')), [client])
  useEffect(() => { void load() }, [load])

  const eligible = agents.filter((a) => !a.identity.retired)
  const nameOf = useMemo(() => new Map(agents.map((a) => [a.identity.id, a.identity.name])), [agents])
  const groups = useMemo(() => {
    const m = new Map<string, AssignmentRecord[]>()
    for (const r of rows ?? []) {
      const key = group === 'agent' ? r.agentId : `${r.target.type} ${r.target.id}`
      m.set(key, [...(m.get(key) ?? []), r])
    }
    return [...m].sort(([a], [b]) => a.localeCompare(b))
  }, [rows, group])

  if (rows === null) return error ? <div className="p-4"><EmptyState variant="error" title="Assignments unavailable" description={error} /></div> : <div className="p-4 font-mono text-xs text-zinc-500">Loading assignments…</div>

  const stage = () => {
    if (!form.agentId || !form.targetId.trim()) return
    setAddList((l) => [...l, { agentId: form.agentId, target: { type: form.type, id: form.targetId.trim(), label: form.label.trim() || undefined }, role: form.role.trim() || undefined }])
    setForm({ ...form, targetId: '', label: '', role: '' })
  }
  const toggleRemove = (id: string) => setRemoveIds((l) => (l.includes(id) ? l.filter((x) => x !== id) : [...l, id]))
  const staged = addList.length + removeIds.length

  const review = async () => {
    setBusy(true)
    setError(null)
    try { setPlan(await client.planLifecycle({ op: 'assignment.update', add: addList, remove: removeIds, note })) } catch (e) { setError(e instanceof Error ? e.message : 'Could not plan') } finally { setBusy(false) }
  }
  const reset = () => { setAddList([]); setRemoveIds([]); setNote(''); setPlan(null); void load() }

  if (plan) {
    return <PlanRunner plan={plan} subjectLabel="assignments" onBack={() => setPlan(null)} onDone={reset} onChanged={() => void load()} />
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 border-b border-zinc-800/80 px-4 py-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
        <span>{rows.length} assignments</span>
        <span>{new Set(rows.map((r) => r.agentId)).size} agents</span>
        <span role="group" aria-label="Group by" className="ml-auto flex gap-1">
          {(['agent', 'target'] as const).map((g) => <button key={g} type="button" aria-pressed={group === g} onClick={() => setGroup(g)} className={`px-2.5 py-1 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 ${group === g ? 'bg-zinc-800 text-zinc-100' : 'hover:text-zinc-300'}`}>By {g}</button>)}
        </span>
      </div>

      <Block title="Assign">
        <div className="grid items-end gap-3 sm:grid-cols-2 lg:grid-cols-[1fr_8rem_1fr_1fr_8rem_auto]">
          <Field label="Agent" htmlFor="as-agent"><select id="as-agent" value={form.agentId} onChange={(e) => setForm({ ...form, agentId: e.target.value })} className={inputCls}><option value="">Choose…</option>{eligible.map((a) => <option key={a.identity.id} value={a.identity.id}>{a.identity.name}</option>)}</select></Field>
          <Field label="Type" htmlFor="as-type"><select id="as-type" value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value as AssignmentTargetType })} className={inputCls}><option value="task">task</option><option value="workstream">workstream</option></select></Field>
          <Field label="Target id" htmlFor="as-target"><input id="as-target" value={form.targetId} onChange={(e) => setForm({ ...form, targetId: e.target.value })} className={inputCls} placeholder="CW-… or ws-…" /></Field>
          <Field label="Label" htmlFor="as-label"><input id="as-label" value={form.label} onChange={(e) => setForm({ ...form, label: e.target.value })} className={inputCls} /></Field>
          <Field label="Role" htmlFor="as-role"><input id="as-role" value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value })} className={inputCls} /></Field>
          <Btn onClick={stage} disabled={!form.agentId || !form.targetId.trim()}>Stage</Btn>
        </div>
      </Block>

      {groups.length === 0 ? <div className="p-4"><EmptyState variant="empty" title="No assignments" description="Assign an agent to a workstream or task above." /></div> : (
        <ul aria-label="Assignments">
          {groups.map(([key, items]) => (
            <li key={key} className="border-b border-zinc-800/80">
              <div className="bg-zinc-900/40 px-4 py-1.5 font-mono text-[11px] uppercase tracking-widest text-zinc-400">{group === 'agent' ? nameOf.get(key) ?? key : key} <span className="text-zinc-500">· {items.length}</span></div>
              <ul>
                {items.map((r) => {
                  const releasing = removeIds.includes(r.id)
                  return (
                    <li key={r.id} className={`flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2 ${releasing ? 'bg-rose-400/5' : ''}`}>
                      <span className={`min-w-0 flex-1 text-[13px] ${releasing ? 'text-zinc-500 line-through' : 'text-zinc-200'}`}>
                        {group === 'agent' ? <><Chip>{r.target.type}</Chip> <span className="ml-1 font-mono">{r.target.id}</span>{r.target.label && <span className="ml-2 text-zinc-500">{r.target.label}</span>}</> : <span className="font-mono">{nameOf.get(r.agentId) ?? r.agentId}</span>}
                      </span>
                      {r.role && <span className="font-mono text-[11px] uppercase tracking-wider text-zinc-500">{r.role}</span>}
                      <span className="font-mono text-[11px] text-zinc-500">{ago(r.since, now)}</span>
                      <Btn onClick={() => toggleRemove(r.id)} aria-label={`${releasing ? 'Keep' : 'Release'} ${r.agentId} ${r.target.id}`}>{releasing ? 'Keep' : 'Release'}</Btn>
                    </li>
                  )
                })}
              </ul>
            </li>
          ))}
        </ul>
      )}

      {staged > 0 && (
        <div className="sticky bottom-0 border-t border-zinc-800 bg-zinc-950">
          <ul aria-label="Staged changes" className="space-y-0.5 border-b border-zinc-800/80 px-5 py-2 text-[13px]">
            {addList.map((d, n) => <li key={`a${n}`} className="flex items-center gap-2 text-emerald-300">+ {nameOf.get(d.agentId) ?? d.agentId} to {d.target.type} {d.target.id}<button type="button" className="ml-auto font-mono text-[11px] uppercase tracking-wider text-zinc-500 hover:text-zinc-300" onClick={() => setAddList((l) => l.filter((_, i) => i !== n))}>unstage</button></li>)}
            {removeIds.map((id) => { const r = rows.find((x) => x.id === id); return r && <li key={id} className="text-rose-300">- {nameOf.get(r.agentId) ?? r.agentId} from {r.target.type} {r.target.id}</li> })}
          </ul>
          <div className="flex flex-wrap items-center gap-3 px-5 py-3">
            <input aria-label="Reason" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Reason (optional)" className={`${inputCls} max-w-xs`} />
            {error && <span role="alert" className="text-[13px] text-rose-300">{error}</span>}
            <span className="ml-auto flex gap-2"><Btn onClick={() => { setAddList([]); setRemoveIds([]) }}>Clear</Btn><Btn tone="primary" onClick={review} disabled={busy}>Review plan ({staged})</Btn></span>
          </div>
        </div>
      )}
    </div>
  )
}
