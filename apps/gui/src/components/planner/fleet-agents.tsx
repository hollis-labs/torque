import { EmptyState } from '@hollis-labs/sysop-ui'
import { useFleetAgents } from '@/hooks/use-fleet-agents'
import { PixelRobot } from '@/components/planner/agent-activity'
import { ago, type AgentState } from '@/lib/agent-activity'
import { cwdTail, fleetCounts, sortFleetAgents, type FleetAgent, type FleetState } from '@/lib/fleet-agents'

const STATE_STYLE: Record<FleetState, { label: string; dot: string; text: string; avatar: AgentState }> = {
  waiting: { label: 'WAITING ON YOU', dot: 'bg-amber-400', text: 'text-amber-300', avatar: 'blocked' },
  busy: { label: 'WORKING', dot: 'bg-emerald-400', text: 'text-emerald-300', avatar: 'working' },
  idle: { label: 'IDLE', dot: 'bg-sky-400', text: 'text-sky-300', avatar: 'idle' },
  unknown: { label: 'UNKNOWN', dot: 'bg-zinc-600', text: 'text-zinc-400', avatar: 'idle' },
}

function Row({ agent, now }: { agent: FleetAgent; now: number }) {
  const s = STATE_STYLE[agent.state]
  const waiting = agent.state === 'waiting'
  return (
    <li className={`flex items-center gap-3 border-b border-zinc-800/80 px-4 py-3 ${waiting ? 'bg-amber-950/20' : ''}`}>
      <PixelRobot id={agent.id} size={36} state={s.avatar} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
          <span className="font-mono text-sm text-zinc-100">{agent.provider}</span>
          <span className={`inline-flex items-center gap-1.5 font-mono text-[10px] tracking-widest ${s.text}`}>
            <span className={`h-1.5 w-1.5 ${s.dot}`} />
            {s.label}
            {agent.stateConfidence && <span className="text-zinc-500">· {agent.stateConfidence}</span>}
          </span>
        </div>
        <div className="truncate font-mono text-xs text-zinc-400" title={agent.cwd}>{cwdTail(agent.cwd)}</div>
        {waiting && agent.waitingFor && <div className="truncate text-xs text-amber-200">{agent.waitingFor}</div>}
      </div>
      <div className="shrink-0 text-right font-mono text-[11px] text-zinc-500">
        <div>{agent.lastActivity ? ago(agent.lastActivity, now) : 'no activity data'}</div>
        <div>pid {agent.pid}</div>
      </div>
    </li>
  )
}

/** Owns the live feed, so the stream and polling exist only while this tab is open. */
export function FleetAgentsPanel() {
  const fleet = useFleetAgents()
  return <FleetAgentsView agents={fleet.agents} error={fleet.error} now={fleet.now} live={fleet.live} />
}

export function FleetAgentsView({ agents, error, now, live }: { agents: FleetAgent[] | null; error: string | null; now: number; live: boolean }) {
  if (agents === null) {
    return error ? (
      <div className="p-4"><EmptyState variant="error" title="Activity source unavailable" description={error} /></div>
    ) : (
      <div className="p-4 font-mono text-xs text-zinc-500">Loading agents…</div>
    )
  }
  const rows = sortFleetAgents(agents)
  const c = fleetCounts(agents)
  return (
    <div>
      <div className="flex flex-wrap gap-x-6 gap-y-1 border-b border-zinc-800/80 px-4 py-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
        <span className={c.waiting ? 'text-amber-300' : ''}>{c.waiting} waiting</span>
        <span>{c.busy} working</span>
        <span>{c.idle} idle</span>
        <span>{c.unknown} unknown</span>
        <span className={live ? 'text-emerald-300' : 'text-zinc-600'}>{live ? '● live' : '○ resyncing'}</span>
        {error && <span className="text-red-300">stale: {error}</span>}
      </div>
      {rows.length === 0 ? (
        <div className="p-4"><EmptyState title="No agents running" description="The activity monitor reports no agent processes on this machine." /></div>
      ) : (
        <ul>{rows.map((a) => <Row key={a.id} agent={a} now={now} />)}</ul>
      )}
    </div>
  )
}
