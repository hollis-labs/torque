import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { EmptyState, Input } from '@hollis-labs/sysop-ui'
import { PixelRobot } from '@/components/planner/agent-activity'
import { ago } from '@/lib/agent-activity'
import { AgentEditor } from './AgentEditor'
import { AssignmentsView } from './AssignmentsView'
import { AuditTrail } from './AuditTrail'
import { CreateWizard } from './CreateWizard'
import { LifecyclePanel } from './LifecyclePanel'
import { TeamsView } from './TeamsView'
import { useAgentsClient } from './context'
import { AgentEditProvider, useAgentEdit } from './edit-context'
import { Btn } from './ui'
import { filterAgents, isPrivileged, policySummary } from './filter'
import type { AgentLiveRef, AgentRecord } from './types'

const LIVE_STYLE: Record<AgentLiveRef['state'], { label: string; dot: string; text: string; avatar: 'blocked' | 'working' | 'idle' }> = {
  waiting: { label: 'WAITING ON YOU', dot: 'bg-amber-400', text: 'text-amber-300', avatar: 'blocked' },
  busy: { label: 'WORKING', dot: 'bg-emerald-400', text: 'text-emerald-300', avatar: 'working' },
  idle: { label: 'IDLE', dot: 'bg-sky-400', text: 'text-sky-300', avatar: 'idle' },
  offline: { label: 'OFFLINE', dot: 'bg-zinc-600', text: 'text-zinc-400', avatar: 'idle' },
}

const FLEET_PATH = '/planner/agents'

function LiveBadge({ live }: { live?: AgentLiveRef }) {
  const s = LIVE_STYLE[live?.state ?? 'offline']
  return (
    <span className={`inline-flex items-center gap-1.5 font-mono text-[10px] tracking-widest ${s.text}`}>
      <span className={`h-1.5 w-1.5 ${s.dot}`} />
      {live ? s.label : 'NOT RUNNING'}
    </span>
  )
}

function Row({ agent, selected, onSelect }: { agent: AgentRecord; selected: boolean; onSelect: () => void }) {
  const { identity, runtime } = agent
  return (
    <li>
      <button
        type="button"
        onClick={onSelect}
        aria-current={selected ? 'true' : undefined}
        className={`flex w-full items-center gap-3 border-b border-zinc-800/80 px-4 py-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-amber-300 ${selected ? 'bg-zinc-900' : 'hover:bg-zinc-900/50'} ${identity.enabled ? '' : 'opacity-60'}`}
      >
        <PixelRobot id={identity.id} size={36} state={LIVE_STYLE[agent.live?.state ?? 'offline'].avatar} />
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
            <span className="font-mono text-sm text-zinc-100">{identity.name}</span>
            {identity.retired && <span className="border border-rose-400/50 px-1 font-mono text-[9px] uppercase tracking-widest text-rose-300">retired</span>}
            {!identity.enabled && !identity.retired && <span className="border border-zinc-700 px-1 font-mono text-[9px] uppercase tracking-widest text-zinc-400">disabled</span>}
            {isPrivileged(agent) && <span className="border border-amber-400/60 px-1 font-mono text-[9px] uppercase tracking-widest text-amber-300">privileged</span>}
          </span>
          <span className="block truncate font-mono text-xs text-zinc-400">
            {runtime.provider}
            {runtime.model ? ` · ${runtime.model}` : ''}
            {runtime.project ? ` · ${runtime.project}` : ''}
          </span>
        </span>
        <LiveBadge live={agent.live} />
      </button>
    </li>
  )
}

const Heading = ({ children }: { children: ReactNode }) => (
  <h3 className="mb-2 font-mono text-[10px] uppercase tracking-widest text-zinc-500">{children}</h3>
)

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="border-b border-zinc-800/80 px-5 py-4">
      <Heading>{title}</Heading>
      {children}
    </section>
  )
}

function Chip({ children, tone = 'border-zinc-700 text-zinc-300' }: { children: ReactNode; tone?: string }) {
  return <span className={`border px-1.5 py-0.5 font-mono text-[11px] ${tone}`}>{children}</span>
}

function Fact({ k, v }: { k: string; v?: ReactNode }) {
  return (
    <div className="flex gap-3 py-0.5 text-[13px]">
      <dt className="w-28 shrink-0 font-mono text-[11px] uppercase tracking-wider text-zinc-500">{k}</dt>
      <dd className="min-w-0 break-words text-zinc-200">{v ?? <span className="text-zinc-500">not reported</span>}</dd>
    </div>
  )
}

function None({ children }: { children: ReactNode }) {
  return <p className="text-[13px] text-zinc-500">{children}</p>
}

export function AgentDetail({ agent, now }: { agent: AgentRecord; now: number }) {
  const { identity, runtime, policy, source, live } = agent
  return (
    <article aria-label={`${identity.name} detail`}>
      <header className="flex items-center gap-4 border-b border-zinc-800/80 px-5 py-4">
        <span className="shrink-0 border border-zinc-700 bg-zinc-950 p-1.5">
          <PixelRobot id={identity.id} size={64} state={LIVE_STYLE[live?.state ?? 'offline'].avatar} />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="truncate font-mono text-lg font-semibold uppercase tracking-wide">{identity.name}</h2>
          <div className="truncate font-mono text-xs text-zinc-500">{identity.id}{identity.urn ? ` · ${identity.urn}` : ' · not registered'}</div>
          {identity.summary && <p className="mt-1 text-[13px] text-zinc-400">{identity.summary}</p>}
        </div>
      </header>

      <Section title="Live state">
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1">
          <LiveBadge live={live} />
          {live && <span className="font-mono text-xs text-zinc-400">{live.sessions} session{live.sessions === 1 ? '' : 's'}</span>}
          {live?.lastActivity != null && <span className="font-mono text-xs text-zinc-400">active {ago(live.lastActivity, now)}</span>}
          <Link to={FLEET_PATH} className="ml-auto font-mono text-[11px] uppercase tracking-widest text-amber-300 hover:underline">open in fleet</Link>
        </div>
      </Section>

      <Section title="Identity">
        <dl>
          <Fact k="Status" v={identity.enabled ? 'Enabled' : 'Disabled'} />
          <Fact k="Roles" v={identity.roles.length ? <span className="flex flex-wrap gap-1.5">{identity.roles.map((r) => <Chip key={r}>{r}</Chip>)}</span> : undefined} />
        </dl>
      </Section>

      <Section title="Runtime">
        <dl>
          <Fact k="Provider" v={runtime.provider} />
          <Fact k="Model" v={runtime.model} />
          <Fact k="Project" v={runtime.project} />
          <Fact k="Worker" v={runtime.worker} />
          <Fact k="Workspace" v={runtime.workspaceMode} />
        </dl>
      </Section>

      <Section title="Tools and skills">
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="w-16 font-mono text-[11px] uppercase tracking-wider text-zinc-500">tools</span>
            {agent.tools.length ? agent.tools.map((t) => <Chip key={t} tone="border-emerald-400/50 text-emerald-300">{t}</Chip>) : <None>None resolved.</None>}
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="w-16 font-mono text-[11px] uppercase tracking-wider text-zinc-500">skills</span>
            {agent.skills.length ? agent.skills.map((t) => <Chip key={t}>{t}</Chip>) : <None>No skills equipped.</None>}
          </div>
        </div>
        <p className="mt-2 text-[12px] text-zinc-500">Tools come from the home project, not the agent.</p>
      </Section>

      <Section title="Permissions and policy">
        <p className={`mb-2 text-[13px] ${isPrivileged(agent) ? 'text-amber-300' : 'text-zinc-300'}`}>{policySummary(agent)}</p>
        <dl>
          <Fact k="Permission" v={policy.permissionMode} />
          <Fact k="Sandbox" v={policy.sandbox} />
          <Fact k="Network" v={policy.network === undefined ? undefined : policy.network ? 'Online' : 'Offline'} />
          <Fact k="Scopes" v={policy.scopes.length ? <span className="flex flex-wrap gap-1.5">{policy.scopes.map((s) => <Chip key={s} tone="border-amber-400/60 text-amber-300">{s}</Chip>)}</span> : 'None'} />
          <Fact k="Rules" v={policy.rules.length ? <span className="flex flex-wrap gap-1.5">{policy.rules.map((s) => <Chip key={s}>{s}</Chip>)}</span> : 'None'} />
        </dl>
      </Section>

      <Section title="Teams">
        {agent.teams.length ? (
          <ul>
            {agent.teams.map((t) => (
              <li key={t.teamId} className="flex items-baseline gap-3 py-0.5 text-[13px]">
                <span className="text-zinc-200">{t.teamName}</span>
                <span className="font-mono text-[11px] uppercase tracking-wider text-zinc-500">{t.role}</span>
              </li>
            ))}
          </ul>
        ) : (
          <None>Not on any team.</None>
        )}
      </Section>

      <Section title="Source definition">
        <dl>
          <Fact k="File" v={<span className="font-mono text-xs">{source.path}</span>} />
          <Fact k="Revision" v={source.revision && <span className="font-mono text-xs">{source.revision}</span>} />
          <Fact k="Updated" v={source.updatedAt ? ago(source.updatedAt, now) : undefined} />
        </dl>
      </Section>
    </article>
  )
}

/** Read-only agent directory: list on the left, detail on the right. No stream hooks; the client is the only data seam. */
export function AgentDirectoryView(props: { now?: number } = {}) {
  // Provide the editor wiring (ActionBus + IdentityConfirm) unless a parent already did.
  const wired = useAgentEdit()
  return wired ? <AgentDirectoryInner {...props} /> : <AgentEditProvider><AgentDirectoryInner {...props} /></AgentEditProvider>
}

type PaneMode = 'view' | 'edit' | 'lifecycle' | 'audit'
type Section = 'agents' | 'teams' | 'assignments'

function AgentDirectoryInner({ now: nowProp }: { now?: number } = {}) {
  const client = useAgentsClient()
  const [agents, setAgents] = useState<AgentRecord[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [provider, setProvider] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [mode, setMode] = useState<PaneMode>('view')
  const [auditVersion, setAuditVersion] = useState(0)
  const [section, setSection] = useState<Section>('agents')
  const [creating, setCreating] = useState<{ cloneOf?: string } | null>(null)
  const [showRetired, setShowRetired] = useState(false)
  const [now] = useState(() => nowProp ?? Date.now())

  useEffect(() => {
    let active = true
    client
      .listAgents()
      .then((rows) => active && (setAgents(rows), setError(null)))
      .catch((e) => active && setError(e instanceof Error ? e.message : 'Agent catalog unavailable'))
    return () => {
      active = false
    }
  }, [client])

  const reload = () => client.listAgents().then((rows) => setAgents(rows), () => undefined)

  const providers = useMemo(() => [...new Set((agents ?? []).map((a) => a.runtime.provider))].sort(), [agents])
  const rows = useMemo(() => filterAgents(agents ?? [], { search, provider }).filter((a) => showRetired || !a.identity.retired || a.identity.id === selectedId), [agents, search, provider, showRetired, selectedId])

  if (agents === null) {
    return error ? (
      <div className="p-4"><EmptyState variant="error" title="Agent catalog unavailable" description={error} /></div>
    ) : (
      <div className="p-4 font-mono text-xs text-zinc-500">Loading agents…</div>
    )
  }

  const selected = rows.find((a) => a.identity.id === selectedId) ?? rows[0] ?? null
  const live = agents.filter((a) => a.live && a.live.state !== 'offline').length

  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 border-b border-zinc-800/80 px-4 py-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
        <span>{agents.length} agents</span>
        <span className={live ? 'text-emerald-300' : ''}>{live} running</span>
        <span>{agents.filter((a) => !a.identity.enabled && !a.identity.retired).length} disabled</span>
        <span>{agents.filter((a) => a.identity.retired).length} retired</span>
        <span className="ml-auto text-zinc-500">sample data · changes need confirmation</span>
      </div>
      <div role="tablist" aria-label="Agent manager" className="flex items-center gap-1 border-b border-zinc-800/80 px-4 py-1.5">
        {(['agents', 'teams', 'assignments'] as const).map((sec) => (
          <button key={sec} type="button" role="tab" aria-selected={section === sec} onClick={() => { setSection(sec); setCreating(null) }} className={`px-2.5 py-1 font-mono text-[11px] uppercase tracking-widest focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 ${section === sec ? 'bg-zinc-800 text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'}`}>{sec}</button>
        ))}
        <Btn className="ml-auto" tone="primary" onClick={() => { setSection('agents'); setCreating({}) }}>New agent</Btn>
      </div>
      {creating && (
        <CreateWizard
          agents={agents}
          cloneOf={creating.cloneOf}
          onClose={() => { setCreating(null); void reload() }}
          onCreated={(a) => { setSelectedId(a.identity.id); setMode('view'); setAuditVersion((v) => v + 1); void reload() }}
        />
      )}
      {!creating && section === 'teams' && <TeamsView agents={agents} onAgentsChanged={() => void reload()} />}
      {!creating && section === 'assignments' && <AssignmentsView agents={agents} now={nowProp} />}
      {!creating && section === 'agents' && (<>
      <div className="flex flex-wrap items-center gap-3 border-b border-zinc-800/80 px-4 py-2">
        <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search agents" aria-label="Search agents" className="max-w-xs" />
        <select value={provider} onChange={(e) => setProvider(e.target.value)} aria-label="Filter by provider" className="h-9 border border-zinc-800 bg-zinc-950 px-2 font-mono text-xs text-zinc-300">
          <option value="">All providers</option>
          {providers.map((p) => <option key={p} value={p}>{p}</option>)}
        </select>
        <label className="flex items-center gap-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500"><input type="checkbox" checked={showRetired} onChange={(e) => setShowRetired(e.target.checked)} /> Show retired</label>
      </div>
      {rows.length === 0 ? (
        <div className="p-4"><EmptyState variant="empty" title="No agents match" description="Clear the search or provider filter." /></div>
      ) : (
        <div className="grid lg:grid-cols-[minmax(18rem,26rem)_minmax(0,1fr)]">
          <ul aria-label="Agents" className="lg:border-r lg:border-zinc-800/80">
            {rows.map((a) => <Row key={a.identity.id} agent={a} selected={a.identity.id === selected?.identity.id} onSelect={() => { setSelectedId(a.identity.id); setMode('view') }} />)}
          </ul>
          {selected && (
            <div>
              <div role="tablist" aria-label="Agent pane" className="flex items-center gap-1 border-b border-zinc-800/80 px-5 py-2">
                {(['view', 'edit', 'lifecycle', 'audit'] as const).map((m) => (
                  <button key={m} type="button" role="tab" aria-selected={mode === m} onClick={() => setMode(m)} className={`px-2.5 py-1 font-mono text-[11px] uppercase tracking-widest focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 ${mode === m ? 'bg-zinc-800 text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'}`}>
                    {m === 'view' ? 'Details' : m === 'edit' ? 'Edit' : m === 'lifecycle' ? 'Lifecycle' : 'Audit'}
                  </button>
                ))}
              </div>
              {mode === 'view' && <AgentDetail agent={selected} now={now} />}
              {mode === 'edit' && (
                <AgentEditor
                  key={selected.identity.id}
                  agent={selected}
                  onClose={() => setMode('view')}
                  onChanged={(next) => {
                    setAgents((cur) => cur && cur.map((a) => (a.identity.id === next.identity.id ? { ...next, live: a.live } : a)))
                    setAuditVersion((v) => v + 1)
                  }}
                />
              )}
              {mode === 'lifecycle' && (
                <LifecyclePanel
                  key={selected.identity.id}
                  agent={selected}
                  onClone={(id) => setCreating({ cloneOf: id })}
                  onChanged={(next) => {
                    setAgents((cur) => cur && cur.map((a) => (a.identity.id === next.identity.id ? { ...next, live: next.live ?? a.live } : a)))
                    setAuditVersion((v) => v + 1)
                    void reload()
                  }}
                />
              )}
              {mode === 'audit' && <AuditTrail agentId={selected.identity.id} version={auditVersion} />}
            </div>
          )}
        </div>
      )}
      </>)}
    </div>
  )
}
