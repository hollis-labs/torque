import { useCallback, useEffect, useMemo, useState } from 'react'
import { EmptyState } from '@hollis-labs/sysop-ui'
import { PixelRobot } from '@/components/planner/agent-activity'
import { useAgentsClient } from './context'
import { Field, inputCls, ListField } from './AgentEditor'
import { blankTeamDraft, computeLifecyclePlan, slugify, teamDraftFrom, TEAM_ROLES, type TeamDraft, type TeamRecord } from './lifecycle'
import { KNOWN_RULES, KNOWN_SCOPES, PERMISSION_MODES, type AgentPlan } from './plan'
import { PlanRunner } from './PlanRunner'
import { Block, Btn, Chip, Heading, RiskBadge } from './ui'
import type { AgentRecord } from './types'

const TONE: Record<string, string> = { coordinator: 'border-amber-400/60 text-amber-300', reviewer: 'border-sky-400/50 text-sky-300' }

function TeamDetail({ team, agents, onEdit, onDelete }: { team: TeamRecord; agents: AgentRecord[]; onEdit: () => void; onDelete: () => void }) {
  const byId = new Map(agents.map((a) => [a.identity.id, a]))
  return (
    <article aria-label={`${team.name} team`}>
      <header className="flex flex-wrap items-center gap-3 border-b border-zinc-800/80 px-5 py-4">
        <div className="min-w-0 flex-1">
          <h2 className="truncate font-mono text-lg font-semibold uppercase tracking-wide">{team.name}</h2>
          <div className="font-mono text-xs text-zinc-500">{team.id} · revision {team.revision}</div>
          {team.summary && <p className="mt-1 text-[13px] text-zinc-400">{team.summary}</p>}
        </div>
        <Btn onClick={onEdit}>Edit team</Btn>
        <Btn tone="danger" onClick={onDelete}>Delete…</Btn>
      </header>
      <Block title={`Members (${team.members.length})`}>
        {team.members.length === 0 ? <p className="text-[13px] text-zinc-500">No members.</p> : (
          <ul aria-label="Members" className="divide-y divide-zinc-800/80 border border-zinc-800/80">
            {team.members.map((m) => {
              const a = byId.get(m.agentId)
              return (
                <li key={m.agentId} className="flex items-center gap-3 px-3 py-2">
                  <PixelRobot id={m.agentId} size={28} state={a?.live?.state === 'busy' ? 'working' : a?.live?.state === 'waiting' ? 'blocked' : 'idle'} />
                  <span className="min-w-0 flex-1"><span className="block truncate font-mono text-[13px] text-zinc-100">{a?.identity.name ?? m.agentId}</span><span className="block truncate font-mono text-[11px] text-zinc-500">{m.agentId}{a?.identity.retired ? ' · retired' : !a?.identity.enabled ? ' · disabled' : ''}</span></span>
                  <Chip tone={TONE[m.role]}>{m.role}</Chip>
                </li>
              )
            })}
          </ul>
        )}
      </Block>
      <Block title="Shared policy">
        <dl className="space-y-1.5 text-[13px]">
          <div className="flex gap-3"><dt className="w-36 shrink-0 font-mono text-[11px] uppercase tracking-wider text-zinc-500">Permission ceiling</dt><dd className={team.policy.permissionCeiling === 'bypass' ? 'text-amber-300' : 'text-zinc-200'}>{team.policy.permissionCeiling}</dd></div>
          <div className="flex gap-3"><dt className="w-36 shrink-0 font-mono text-[11px] uppercase tracking-wider text-zinc-500">Scope ceiling</dt><dd className="flex flex-wrap gap-1.5">{team.policy.scopesCeiling.length ? team.policy.scopesCeiling.map((s) => <Chip key={s} tone="border-amber-400/60 text-amber-300">{s}</Chip>) : <span className="text-zinc-500">None</span>}</dd></div>
          <div className="flex gap-3"><dt className="w-36 shrink-0 font-mono text-[11px] uppercase tracking-wider text-zinc-500">Rules</dt><dd className="flex flex-wrap gap-1.5">{team.policy.rules.length ? team.policy.rules.map((s) => <Chip key={s}>{s}</Chip>) : <span className="text-zinc-500">None</span>}</dd></div>
          <div className="flex gap-3"><dt className="w-36 shrink-0 font-mono text-[11px] uppercase tracking-wider text-zinc-500">Reports to</dt><dd className="text-zinc-200">{team.policy.reportTo ?? <span className="text-zinc-500">not set</span>}</dd></div>
        </dl>
      </Block>
    </article>
  )
}

function TeamEditor({ team, agents, onCancel, onDone }: { team?: TeamRecord; agents: AgentRecord[]; onCancel: () => void; onDone: (t?: TeamRecord) => void }) {
  const client = useAgentsClient()
  const [draft, setDraft] = useState<TeamDraft>(() => (team ? teamDraftFrom(team) : blankTeamDraft()))
  const [id, setId] = useState('')
  const [idTouched, setIdTouched] = useState(false)
  const [note, setNote] = useState('')
  const [plan, setPlan] = useState<AgentPlan | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pick, setPick] = useState('')
  const [teams, setTeams] = useState<TeamRecord[]>([])
  useEffect(() => { client.listTeams().then(setTeams, () => undefined) }, [client])

  const teamId = team?.id ?? (idTouched ? id : `tm_${slugify(draft.name).replace(/-/g, '_')}`)
  const req = (team ? { op: 'team.update' as const, teamId, team: draft } : { op: 'team.create' as const, teamId, team: draft })
  const live = useMemo(
    () => computeLifecyclePlan({ ...req, note, baseRevision: team?.revision }, { agents, teams, assignments: [] }, { id: 'preview', now: 0, actor: { id: 'preview' } }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [agents, teams, draft, note, teamId, team],
  )
  const errors = live.issues.filter((i) => i.level === 'error' && !(!team && !draft.members.length && false))
  const addable = agents.filter((a) => !a.identity.retired && !draft.members.some((m) => m.agentId === a.identity.id))
  const setPolicy = <K extends keyof TeamDraft['policy']>(k: K, v: TeamDraft['policy'][K]) => setDraft((d) => ({ ...d, policy: { ...d.policy, [k]: v } }))

  const review = async () => {
    setBusy(true)
    setError(null)
    try {
      setPlan(await client.planLifecycle({ ...req, note, baseRevision: team?.revision }))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not plan')
    } finally {
      setBusy(false)
    }
  }

  if (plan) {
    return <PlanRunner plan={plan} subjectLabel={draft.name || teamId} onBack={() => setPlan(null)} onDone={() => onDone()} onChanged={(o) => o.status === 'applied' && onDone(o.team)} />
  }
  return (
    <article aria-label={team ? `Edit ${team.name}` : 'New team'}>
      <header className="flex flex-wrap items-center gap-3 border-b border-zinc-800/80 px-5 py-3">
        <h2 className="font-mono text-sm font-semibold uppercase tracking-wide">{team ? `Edit ${team.name}` : 'New team'}</h2>
        <span className="ml-auto flex items-center gap-2"><RiskBadge risk={live.risk} /><Btn onClick={onCancel}>Close</Btn></span>
      </header>
      <Block title="Team">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Name" htmlFor="tm-name"><input id="tm-name" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} className={inputCls} /></Field>
          <Field label="Id" htmlFor="tm-id"><input id="tm-id" value={teamId} disabled={!!team} onChange={(e) => { setId(e.target.value); setIdTouched(true) }} className={inputCls} /></Field>
          <div className="sm:col-span-2"><Field label="Summary" htmlFor="tm-summary"><input id="tm-summary" value={draft.summary} onChange={(e) => setDraft({ ...draft, summary: e.target.value })} className={inputCls} /></Field></div>
        </div>
      </Block>
      <Block title={`Members (${draft.members.length})`}>
        <ul aria-label="Team members" className="mb-3 divide-y divide-zinc-800/80 border border-zinc-800/80">
          {draft.members.map((m) => (
            <li key={m.agentId} className="flex items-center gap-3 px-3 py-1.5">
              <span className="min-w-0 flex-1 truncate font-mono text-[13px] text-zinc-100">{agents.find((a) => a.identity.id === m.agentId)?.identity.name ?? m.agentId}</span>
              <select aria-label={`Role for ${m.agentId}`} value={m.role} onChange={(e) => setDraft({ ...draft, members: draft.members.map((x) => (x.agentId === m.agentId ? { ...x, role: e.target.value } : x)) })} className="h-8 border border-zinc-800 bg-zinc-950 px-2 font-mono text-xs text-zinc-200">
                {[...new Set([...TEAM_ROLES, m.role])].map((r) => <option key={r}>{r}</option>)}
              </select>
              <Btn onClick={() => setDraft({ ...draft, members: draft.members.filter((x) => x.agentId !== m.agentId) })} aria-label={`Remove ${m.agentId}`}>Remove</Btn>
            </li>
          ))}
          {draft.members.length === 0 && <li className="px-3 py-2 text-[13px] text-zinc-500">No members yet.</li>}
        </ul>
        <div className="flex items-center gap-2">
          <select aria-label="Add member" value={pick} onChange={(e) => setPick(e.target.value)} className={`${inputCls} max-w-xs`}>
            <option value="">Add an agent…</option>
            {addable.map((a) => <option key={a.identity.id} value={a.identity.id}>{a.identity.name}</option>)}
          </select>
          <Btn disabled={!pick} onClick={() => { setDraft({ ...draft, members: [...draft.members, { agentId: pick, role: draft.members.length ? 'worker' : 'coordinator' }] }); setPick('') }}>Add</Btn>
        </div>
      </Block>
      <Block title="Shared policy">
        <div className="space-y-3">
          <Field label="Permission ceiling" htmlFor="tm-perm"><select id="tm-perm" value={draft.policy.permissionCeiling} onChange={(e) => setPolicy('permissionCeiling', e.target.value)} className={`${inputCls} max-w-xs`}>{PERMISSION_MODES.map((p) => <option key={p}>{p}</option>)}</select></Field>
          <Field label="Scope ceiling"><ListField label="Scope ceiling" values={draft.policy.scopesCeiling} suggestions={KNOWN_SCOPES} tone="border-amber-400/60 bg-amber-400/10 text-amber-300" onChange={(v) => setPolicy('scopesCeiling', v)} /></Field>
          <Field label="Team rules"><ListField label="Team rules" values={draft.policy.rules} suggestions={KNOWN_RULES} onChange={(v) => setPolicy('rules', v)} /></Field>
          <Field label="Reports to" htmlFor="tm-report">
            <select id="tm-report" value={draft.policy.reportTo ?? ''} onChange={(e) => setPolicy('reportTo', e.target.value || undefined)} className={`${inputCls} max-w-xs`}>
              <option value="">not set</option>
              {agents.filter((a) => !a.identity.retired).map((a) => <option key={a.identity.id} value={a.identity.id}>{a.identity.name}</option>)}
            </select>
          </Field>
        </div>
      </Block>
      <Block title="Reason">
        <Field label={live.risk === 'high' ? 'Reason (required for high risk)' : 'Reason (optional)'} htmlFor="tm-note" issues={live.issues.filter((i) => i.field === 'note')}><input id="tm-note" value={note} onChange={(e) => setNote(e.target.value)} className={inputCls} /></Field>
        {live.issues.filter((i) => i.field !== 'note').map((i, n) => <p key={n} className={`mt-1.5 text-[12px] ${i.level === 'error' ? 'text-rose-300' : 'text-amber-300'}`}>{i.message}</p>)}
      </Block>
      {error && <div role="alert" className="border-b border-rose-400/30 bg-rose-400/5 px-5 py-2 text-[13px] text-rose-200">{error}</div>}
      <div className="sticky bottom-0 flex items-center gap-3 border-t border-zinc-800 bg-zinc-950 px-5 py-3">
        <span className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">{live.lifecycle?.items.length ?? 0} change{live.lifecycle?.items.length === 1 ? '' : 's'}</span>
        <Btn className="ml-auto" tone="primary" onClick={review} disabled={busy || errors.length > 0}>Review plan</Btn>
      </div>
    </article>
  )
}

type Pane = { kind: 'view' } | { kind: 'edit' } | { kind: 'new' } | { kind: 'delete' }

/** Teams: members, roles and shared policy. Every change is a lifecycle plan with a step-up. */
export function TeamsView({ agents, onAgentsChanged }: { agents: AgentRecord[]; onAgentsChanged?: () => void }) {
  const client = useAgentsClient()
  const [teams, setTeams] = useState<TeamRecord[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [pane, setPane] = useState<Pane>({ kind: 'view' })
  const [note, setNote] = useState('')
  const [plan, setPlan] = useState<AgentPlan | null>(null)

  const load = useCallback(() => client.listTeams().then((t) => (setTeams(t), setError(null)), (e) => setError(e instanceof Error ? e.message : 'Teams unavailable')), [client])
  useEffect(() => { void load() }, [load])

  if (teams === null) return error ? <div className="p-4"><EmptyState variant="error" title="Teams unavailable" description={error} /></div> : <div className="p-4 font-mono text-xs text-zinc-500">Loading teams…</div>
  const selected = teams.find((t) => t.id === selectedId) ?? teams[0] ?? null
  const done = (t?: TeamRecord) => { void load(); onAgentsChanged?.(); if (t) setSelectedId(t.id); setPane({ kind: 'view' }); setPlan(null); setNote('') }

  const planDelete = async () => {
    if (!selected) return
    try { setPlan(await client.planLifecycle({ op: 'team.delete', teamId: selected.id, note, baseRevision: selected.revision })) } catch (e) { setError(e instanceof Error ? e.message : 'Could not plan') }
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 border-b border-zinc-800/80 px-4 py-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
        <span>{teams.length} teams</span>
        <span>{teams.reduce((n, t) => n + t.members.length, 0)} memberships</span>
        <Btn className="ml-auto" tone="primary" onClick={() => setPane({ kind: 'new' })}>New team</Btn>
      </div>
      <div className="grid lg:grid-cols-[minmax(16rem,22rem)_minmax(0,1fr)]">
        <ul aria-label="Teams" className="lg:border-r lg:border-zinc-800/80">
          {teams.map((t) => (
            <li key={t.id}>
              <button type="button" onClick={() => { setSelectedId(t.id); setPane({ kind: 'view' }); setPlan(null) }} aria-current={t.id === selected?.id ? 'true' : undefined} className={`flex w-full items-baseline gap-3 border-b border-zinc-800/80 px-4 py-3 text-left focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-amber-300 ${t.id === selected?.id ? 'bg-zinc-900' : 'hover:bg-zinc-900/50'}`}>
                <span className="min-w-0 flex-1"><span className="block truncate font-mono text-sm text-zinc-100">{t.name}</span><span className="block truncate font-mono text-xs text-zinc-500">{t.id}</span></span>
                <span className="font-mono text-[11px] text-zinc-400">{t.members.length} members</span>
              </button>
            </li>
          ))}
          {teams.length === 0 && <li className="p-4"><EmptyState variant="empty" title="No teams" description="Create a team to share a policy across agents." /></li>}
        </ul>
        <div>
          {pane.kind === 'new' && <TeamEditor key="new" agents={agents} onCancel={() => setPane({ kind: 'view' })} onDone={done} />}
          {pane.kind === 'edit' && selected && <TeamEditor key={selected.id} team={selected} agents={agents} onCancel={() => setPane({ kind: 'view' })} onDone={done} />}
          {pane.kind === 'delete' && selected && (plan ? (
            <PlanRunner plan={plan} subjectLabel={selected.name} onBack={() => setPlan(null)} onDone={() => done()} onChanged={() => { void load(); onAgentsChanged?.() }} />
          ) : (
            <Block title={`Delete ${selected.name}`}>
              <p className="mb-3 text-[13px] text-zinc-300">All {selected.members.length} members lose this membership and the shared policy stops applying. High risk: a reason is required.</p>
              <Field label="Reason (required)" htmlFor="tm-del-note"><input id="tm-del-note" value={note} onChange={(e) => setNote(e.target.value)} className={inputCls} /></Field>
              {error && <p role="alert" className="mt-2 text-[13px] text-rose-300">{error}</p>}
              <div className="mt-3 flex gap-2"><Btn onClick={() => setPane({ kind: 'view' })}>Cancel</Btn><Btn tone="danger" disabled={!note.trim()} onClick={planDelete}>Review delete plan</Btn></div>
            </Block>
          ))}
          {pane.kind === 'view' && (selected ? <TeamDetail team={selected} agents={agents} onEdit={() => setPane({ kind: 'edit' })} onDelete={() => { setNote(''); setPlan(null); setPane({ kind: 'delete' }) }} /> : <div className="p-4"><Heading>Teams</Heading><p className="text-[13px] text-zinc-500">Select or create a team.</p></div>)}
        </div>
      </div>
    </div>
  )
}
