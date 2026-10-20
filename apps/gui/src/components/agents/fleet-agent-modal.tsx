import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@hollis-labs/sysop-ui'
import { PixelRobot } from '@/components/agents/agent-activity'
import { MailBadge, Score } from '@/components/agents/fleet-parts'
import { FLEET_STATE_STYLE } from '@/components/agents/fleet-style'
import { StateTimeline } from '@/components/agents/fleet-timeline'
import { LinkText } from '@/console/autolink'
import { FleetMessageActions } from '@/console/messaging/FleetActions'
import { ago } from '@/lib/agent-activity'
import { cwdName, formatDuration, whatNow, stateDurations, stateSegments, timelineWindow, workingShare, type FleetAgent, type FleetTransition } from '@/lib/fleet-agents'

function Fact({ label, value, title, link }: { label: string; value: string; title?: string; link?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="truncate font-mono text-sm tabular-nums text-zinc-100" title={title ?? value}>{link ? <LinkText surface="agents" text={value} /> : value}</div>
      <div className="font-mono text-[9px] uppercase tracking-widest text-zinc-500">{label}</div>
    </div>
  )
}

const KIND_LABEL: Record<FleetTransition['kind'], string> = { seen: 'first seen', started: 'started', state: 'changed' }

export function FleetAgentModal({ agent, score, rank, entries, now, onClose }: { agent: FleetAgent; score: number; rank: number; entries: FleetTransition[]; now: number; onClose: () => void }) {
  const s = FLEET_STATE_STYLE[agent.state]
  const windowMs = timelineWindow(entries, now)
  const share = workingShare(entries, now, windowMs)
  const observed = stateDurations(stateSegments(entries, now, windowMs))
  const observedMs = observed.waiting + observed.busy + observed.idle + observed.unknown
  const history = [...entries].reverse()
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <div className="flex items-center gap-4">
            <span className="border-2 border-zinc-600 bg-zinc-950 p-1.5">
              <PixelRobot id={agent.id} size={72} state={s.avatar} />
            </span>
            <div className="min-w-0">
              <DialogTitle className="truncate font-mono uppercase tracking-wide">{cwdName(agent.cwd) || agent.provider}</DialogTitle>
              <DialogDescription className="truncate font-mono text-[11px] tracking-wider" title={agent.cwd}>
                {agent.provider}{agent.model ? ` · ${agent.model}` : ''} · {agent.cwd || 'no working directory reported'}
              </DialogDescription>
              <div className="truncate font-mono text-[11px] tracking-wider text-zinc-400">
                {agent.workstream ? `${agent.workstream.name ?? 'workstream'} · ${agent.workstream.id}` : 'no workstream'} · rank #{rank}
              </div>
              <div className={`mt-1 inline-flex items-center gap-1.5 font-mono text-[10px] tracking-widest ${s.text}`}>
                <span className={`h-1.5 w-1.5 ${s.dot}`} />
                {s.label}
                {agent.stateConfidence && <span className="text-zinc-500">· {agent.stateConfidence}</span>}
              </div>
            </div>
            <div className="ml-auto flex shrink-0 items-start gap-4 text-right">
              <MailBadge count={agent.messages} agent={agent} />
              <div>
                <div className="text-xl text-amber-200"><Score value={score} /></div>
                <div className="font-mono text-[9px] uppercase tracking-widest text-zinc-500">score</div>
              </div>
            </div>
          </div>
        </DialogHeader>

        {agent.state === 'waiting' && (
          <p className="border border-amber-500/40 bg-amber-950/20 px-3 py-2 text-[13px] text-amber-200">
            Blocked on you{agent.waitingFor ? <>: <LinkText surface="agents" text={agent.waitingFor} /></> : ''}.
          </p>
        )}

        <div className="grid grid-cols-2 gap-3 border-y border-border py-3 sm:grid-cols-4">
          {agent.lastAction && <Fact label="doing" value={whatNow(agent, now)} link />}
          <Fact label="up for" value={agent.startedAt ? formatDuration(now - agent.startedAt) : '—'} title={agent.startedAt ? new Date(agent.startedAt).toLocaleString() : undefined} />
          <Fact label="last activity" value={agent.lastActivity ? ago(agent.lastActivity, now) : '—'} title={agent.lastActivity ? new Date(agent.lastActivity).toLocaleString() : undefined} />
          <Fact label="working (observed)" value={share === null ? '—' : `${share}%`} />
          <Fact label="state changes seen" value={String(entries.filter((e) => e.kind === 'state').length)} />
          <Fact label="pid" value={agent.pid ? String(agent.pid) : '—'} />
          <Fact label="session" value={agent.sessionId ?? '—'} />
          <Fact label="agent id" value={agent.id} />
          <Fact label="observed for" value={observedMs > 0 ? formatDuration(observedMs) : '—'} />
        </div>

        <FleetMessageActions agent={agent} onBeforeChat={onClose} />

        <section aria-label="Observed state timeline" className="mt-1">
          <h3 className="mb-1 font-mono text-[10px] uppercase tracking-widest text-zinc-500">Last {formatDuration(windowMs)} watched</h3>
          <StateTimeline entries={entries} now={now} windowMs={windowMs} width={240} height={28} fluid />
          <p className="mt-1 text-[11px] text-muted-foreground">Only what this page has seen since it opened (up to 30 minutes); the source keeps no history.</p>
        </section>

        <section aria-label="Observed changes" className="mt-3">
          <h3 className="mb-1 font-mono text-[10px] uppercase tracking-widest text-zinc-500">Changes seen</h3>
          <ul className="max-h-48 overflow-auto border border-border">
            {history.length === 0 && <li className="px-3 py-2 text-[13px] text-muted-foreground">Nothing observed yet.</li>}
            {history.map((h, i) => {
              const hs = FLEET_STATE_STYLE[h.state]
              return (
                <li key={`${h.at}-${i}`} className="flex items-baseline gap-3 border-b border-border px-3 py-1.5 last:border-b-0">
                  <span className="w-20 shrink-0 whitespace-nowrap font-mono text-[10px] uppercase tracking-wider text-zinc-500">{KIND_LABEL[h.kind]}</span>
                  <span className={`shrink-0 font-mono text-[10px] tracking-widest ${hs.text}`}>{hs.label}</span>
                  <span className="min-w-0 flex-1 truncate text-[13px]">{h.waitingFor ? <LinkText surface="agents" text={h.waitingFor} /> : ''}</span>
                  <span className="shrink-0 font-mono text-[10px] text-zinc-500">{ago(h.at, now)}</span>
                </li>
              )
            })}
          </ul>
        </section>

        <section aria-label="How sure the source is" className="mt-3">
          <h3 className="mb-1 font-mono text-[10px] uppercase tracking-widest text-zinc-500">Source confidence</h3>
          <div className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-[11px]">
            {Object.entries(agent.confidence).length === 0 && <span className="text-zinc-500">not reported</span>}
            {Object.entries(agent.confidence).map(([field, tier]) => (
              <span key={field} className="text-zinc-400">
                {field} <span className={tier === 'exact' ? 'text-emerald-300' : tier === 'derived' ? 'text-sky-300' : 'text-amber-300'}>{tier}</span>
              </span>
            ))}
          </div>
        </section>
      </DialogContent>
    </Dialog>
  )
}
