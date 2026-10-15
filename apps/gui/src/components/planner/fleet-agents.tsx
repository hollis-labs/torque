import { useState } from 'react'
import { Crown, Zap } from 'lucide-react'
import { EmptyState } from '@hollis-labs/sysop-ui'
import { MailBadge, ModelTag, Score, Stat } from '@/components/planner/fleet-parts'
import { ActivityBars, PixelRobot, useFlip } from '@/components/planner/agent-activity'
import { FleetAgentModal } from '@/components/planner/fleet-agent-modal'
import { FLEET_STATE_STYLE } from '@/components/planner/fleet-style'
import { StateTimeline } from '@/components/planner/fleet-timeline'
import { useFleetAgents } from '@/hooks/use-fleet-agents'
import { busySeries, whatNow, cwdName, cwdTail, fleetCounts, formatDuration, groupFleet, timelineWindow, workingShare, type FleetAgent, type FleetHistory } from '@/lib/fleet-agents'

function Row({ agent, rank, score, leader, entries, now, onOpen }: { agent: FleetAgent; rank: number; score: number; leader: boolean; entries: FleetHistory[string]; now: number; onOpen: () => void }) {
  const s = FLEET_STATE_STYLE[agent.state]
  const waiting = agent.state === 'waiting'
  const name = cwdName(agent.cwd) || agent.provider
  const top = leader
  const windowMs = timelineWindow(entries, now)
  const share = workingShare(entries, now, windowMs)
  return (
    <li data-flip={agent.id} className="border-b border-border">
      <button
        type="button"
        onClick={onOpen}
        aria-label={`Open ${name}`}
        className={`group flex w-full flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 text-left hover:bg-muted/50 focus-visible:bg-muted/50 focus-visible:outline-none ${waiting ? 'bg-amber-950/20' : ''} ${agent.state === 'unknown' ? 'opacity-70' : ''}`}
      >
        <span className={`w-8 shrink-0 text-center font-mono text-xs ${top ? 'text-amber-300' : 'text-zinc-500'}`}>
          {top ? <Crown className="mx-auto h-4 w-4" aria-label="Top of the leaderboard" /> : `#${rank}`}
        </span>
        <span className="shrink-0 border-2 border-zinc-700 bg-zinc-950 p-1 group-hover:border-zinc-500">
          <PixelRobot id={agent.id} size={40} state={s.avatar} />
        </span>
        <span className="min-w-0 flex-1 basis-48">
          <span className="flex items-center gap-2">
            <span className="truncate font-mono text-sm font-semibold uppercase tracking-wide">{name}</span>
            <ModelTag agent={agent} />
          </span>
          <span className="mt-0.5 block truncate text-[12px] text-muted-foreground" title={agent.cwd}>
            {waiting && agent.waitingFor ? <span className="text-amber-200">{agent.waitingFor}</span> : whatNow(agent, now)}
            <span className="text-zinc-600"> · {cwdTail(agent.cwd)}</span>
          </span>
          <span className={`inline-flex items-center gap-1.5 font-mono text-[10px] tracking-widest ${s.text}`}>
            <span className={`h-1.5 w-1.5 ${s.dot} ${agent.state === 'busy' ? 'agent-pulse-bar' : ''}`} />
            {s.label}
            {agent.stateConfidence && <span className="text-zinc-500">· {agent.stateConfidence}</span>}
          </span>
        </span>
        <span className="hidden gap-4 sm:flex">
          <Stat label="up" value={agent.startedAt ? formatDuration(now - agent.startedAt) : '—'} />
          <Stat label="working" value={share === null ? '—' : `${share}%`} />
        </span>
        <span className="hidden md:flex md:flex-col md:gap-1">
          <ActivityBars series={busySeries(entries, now, windowMs)} state={s.avatar} scaleMax={100} height={22} />
          <StateTimeline entries={entries} now={now} windowMs={windowMs} height={6} />
        </span>
        <MailBadge count={agent.messages} />
        <span className="w-20 shrink-0 text-right">
          <span className="block text-sm text-amber-200">
            <Score value={score} />
          </span>
          <span className="block font-mono text-[9px] uppercase tracking-widest text-zinc-500">score</span>
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
  const groups = groupFleet(agents ?? [], history, now)
  const ref = useFlip(groups.map((g) => `${g.name}:${g.agents.map((a) => a.agent.id).join(',')}`).join('|'))
  if (agents === null) {
    return error ? (
      <div className="p-4"><EmptyState variant="error" title="Activity source unavailable" description={error} /></div>
    ) : (
      <div className="p-4 font-mono text-xs text-zinc-500">Loading agents…</div>
    )
  }
  const c = fleetCounts(agents)
  const leaderId = groups.flatMap((g) => g.agents).reduce<{ id: string | null; score: number }>((best, a) => (a.score > best.score && a.agent.state !== 'unknown' ? { id: a.agent.id, score: a.score } : best), { id: null, score: -1 }).id
  const open = groups.flatMap((g) => g.agents).find((a) => a.agent.id === openId) ?? null
  return (
    <div ref={ref} className="agent-arcade">
      <div className="flex flex-wrap items-center gap-3 px-4 py-3">
        <p className="text-[13px] text-muted-foreground">Who is doing what, grouped by workstream and ranked on a provisional score.</p>
        <span className="ml-auto inline-flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-[10px] uppercase tracking-widest text-zinc-400">
          <span className={c.waiting ? 'text-amber-300' : ''}>{c.waiting} waiting</span>
          <span className="inline-flex items-center gap-1.5"><Zap className="h-3 w-3 text-emerald-400" />{c.busy}/{agents.length} working</span>
          <span>{c.idle} idle</span>
          <span>{c.unknown} unknown</span>
          <span className={live ? 'text-emerald-300' : 'text-zinc-600'}>{live ? '● live' : '○ resyncing'}</span>
          {error && <span className="text-red-300">stale: {error}</span>}
        </span>
      </div>
      {groups.length === 0 && (
        <div className="p-4"><EmptyState variant="empty" title="No agents running" description="The activity monitor reports no agent processes on this machine." /></div>
      )}
      {groups.map((g) => (
        <section key={g.workstreamId ?? `dir:${g.name}`} aria-label={g.name} data-flip={`group:${g.name}`}>
          <h2 className="flex items-center gap-3 border-y border-border bg-zinc-950/60 px-4 py-1.5 font-mono text-[11px] uppercase tracking-widest text-zinc-300">
            {g.name}
            {g.workstreamId && <span className="normal-case tracking-normal text-zinc-500" title="Workstream id">{g.workstreamId}</span>}
            <span className="text-zinc-600">{g.agents.length} agent{g.agents.length === 1 ? '' : 's'}</span>
            <span className="ml-auto text-amber-200/80"><Score value={g.score} /></span>
          </h2>
          <ul aria-label={`${g.name} agents`}>
            {g.agents.map((a) => (
              <Row key={a.agent.id} agent={a.agent} rank={a.rank} score={a.score} leader={a.agent.id === leaderId} entries={history[a.agent.id] ?? []} now={now} onOpen={() => setOpenId(a.agent.id)} />
            ))}
          </ul>
        </section>
      ))}
      {open && <FleetAgentModal agent={open.agent} score={open.score} rank={open.rank} entries={history[open.agent.id] ?? []} now={now} onClose={() => setOpenId(null)} />}
    </div>
  )
}
