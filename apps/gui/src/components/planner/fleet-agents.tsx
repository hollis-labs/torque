import { useState } from 'react'
import { EmptyState } from '@hollis-labs/sysop-ui'
import { PixelRobot, useFlip } from '@/components/planner/agent-activity'
import { FleetAgentModal } from '@/components/planner/fleet-agent-modal'
import { FLEET_STATE_STYLE } from '@/components/planner/fleet-style'
import { StateTimeline } from '@/components/planner/fleet-timeline'
import { useFleetAgents } from '@/hooks/use-fleet-agents'
import { ago } from '@/lib/agent-activity'
import { cwdName, cwdTail, fleetCounts, formatDuration, sortFleetAgents, timelineWindow, type FleetAgent, type FleetHistory } from '@/lib/fleet-agents'

function Row({ agent, entries, now, onOpen }: { agent: FleetAgent; entries: FleetHistory[string]; now: number; onOpen: () => void }) {
  const s = FLEET_STATE_STYLE[agent.state]
  const waiting = agent.state === 'waiting'
  const name = cwdName(agent.cwd) || agent.provider
  return (
    <li data-flip={agent.id} className="border-b border-zinc-800/80">
      <button
        type="button"
        onClick={onOpen}
        aria-label={`Open ${name}`}
        className={`group flex w-full flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 text-left hover:bg-muted/50 focus-visible:bg-muted/50 focus-visible:outline-none ${waiting ? 'bg-amber-950/20' : ''}`}
      >
        <span className="shrink-0 border-2 border-zinc-700 bg-zinc-950 p-1 group-hover:border-zinc-500">
          <PixelRobot id={agent.id} size={36} state={s.avatar} />
        </span>
        <span className="min-w-0 flex-1 basis-48">
          <span className="flex items-center gap-2">
            <span className="truncate font-mono text-sm font-semibold tracking-wide text-zinc-100">{name}</span>
            <span className="shrink-0 font-mono text-[10px] uppercase tracking-wider text-zinc-500">{agent.provider}</span>
          </span>
          <span className={`inline-flex items-center gap-1.5 font-mono text-[10px] tracking-widest ${s.text}`}>
            <span className={`h-1.5 w-1.5 ${s.dot} ${agent.state === 'busy' ? 'agent-pulse-bar' : ''}`} />
            {s.label}
            {agent.stateConfidence && <span className="text-zinc-500">· {agent.stateConfidence}</span>}
          </span>
          <span className="block truncate font-mono text-xs text-zinc-400" title={agent.cwd}>{cwdTail(agent.cwd)}</span>
          {waiting && agent.waitingFor && <span className="block truncate text-xs text-amber-200">{agent.waitingFor}</span>}
        </span>
        <span className="hidden gap-4 font-mono sm:flex">
          <span className="min-w-[3.25rem]">
            <span className="block text-sm tabular-nums text-zinc-100">{agent.startedAt ? formatDuration(now - agent.startedAt) : '—'}</span>
            <span className="block text-[9px] uppercase tracking-widest text-zinc-500">up</span>
          </span>
        </span>
        <span className="hidden md:block">
          <StateTimeline entries={entries} now={now} windowMs={timelineWindow(entries, now)} />
        </span>
        <span className="w-24 shrink-0 text-right font-mono text-[11px] text-zinc-500">
          <span className="block">{agent.lastActivity ? ago(agent.lastActivity, now) : 'no activity data'}</span>
          <span className="block">pid {agent.pid}</span>
        </span>
      </button>
    </li>
  )
}

/** Owns the live feed, so the stream and polling exist only while this tab is open. */
export function FleetAgentsPanel() {
  const fleet = useFleetAgents()
  return <FleetAgentsView agents={fleet.agents} history={fleet.history} error={fleet.error} now={fleet.now} live={fleet.live} />
}

export function FleetAgentsView({ agents, history, error, now, live }: { agents: FleetAgent[] | null; history: FleetHistory; error: string | null; now: number; live: boolean }) {
  const [openId, setOpenId] = useState<string | null>(null)
  const rows = sortFleetAgents(agents ?? [])
  const ref = useFlip(rows.map((a) => a.id).join('|'))
  if (agents === null) {
    return error ? (
      <div className="p-4"><EmptyState variant="error" title="Activity source unavailable" description={error} /></div>
    ) : (
      <div className="p-4 font-mono text-xs text-zinc-500">Loading agents…</div>
    )
  }
  const c = fleetCounts(agents)
  const open = rows.find((a) => a.id === openId) ?? null
  return (
    <div ref={ref} className="agent-arcade">
      <div className="flex flex-wrap gap-x-6 gap-y-1 border-b border-zinc-800/80 px-4 py-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
        <span className={c.waiting ? 'text-amber-300' : ''}>{c.waiting} waiting</span>
        <span>{c.busy} working</span>
        <span>{c.idle} idle</span>
        <span>{c.unknown} unknown</span>
        <span className={live ? 'text-emerald-300' : 'text-zinc-600'}>{live ? '● live' : '○ resyncing'}</span>
        {error && <span className="text-red-300">stale: {error}</span>}
      </div>
      {rows.length === 0 ? (
        <div className="p-4"><EmptyState variant="empty" title="No agents running" description="The activity monitor reports no agent processes on this machine." /></div>
      ) : (
        <ul>{rows.map((a) => <Row key={a.id} agent={a} entries={history[a.id] ?? []} now={now} onOpen={() => setOpenId(a.id)} />)}</ul>
      )}
      {open && <FleetAgentModal agent={open} entries={history[open.id] ?? []} now={now} onClose={() => setOpenId(null)} />}
    </div>
  )
}
