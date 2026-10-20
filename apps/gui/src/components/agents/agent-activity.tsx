import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Crown, Zap } from 'lucide-react'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@hollis-labs/sysop-ui'
import { ago, groupAgents, robotSprite, successRate, type AgentActivity, type AgentState } from '@/lib/agent-activity'

type Scored = AgentActivity & { score: number; rank: number }

const STATE_STYLE: Record<AgentState, { label: string; dot: string; text: string }> = {
  working: { label: 'WORKING', dot: 'bg-emerald-400', text: 'text-emerald-300' },
  idle: { label: 'IDLE', dot: 'bg-sky-400', text: 'text-sky-300' },
  blocked: { label: 'BLOCKED', dot: 'bg-amber-400', text: 'text-amber-300' },
  offline: { label: 'OFFLINE', dot: 'bg-zinc-600', text: 'text-zinc-500' },
}

const HISTORY_STYLE: Record<string, string> = {
  done: 'text-emerald-300',
  failed: 'text-red-300',
  started: 'text-sky-300',
  note: 'text-zinc-400',
}

/** Reorder motion: rows glide from their old slot to the new one. */
export function useFlip(order: string) {
  const ref = useRef<HTMLDivElement>(null)
  const prev = useRef(new Map<string, number>())
  useLayoutEffect(() => {
    const root = ref.current
    if (!root) return
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    const next = new Map<string, number>()
    root.querySelectorAll<HTMLElement>('[data-flip]').forEach((el) => {
      const top = el.getBoundingClientRect().top
      next.set(el.dataset.flip!, top)
      const was = prev.current.get(el.dataset.flip!)
      if (reduce || was === undefined || Math.abs(was - top) < 1) return
      el.style.transition = 'none'
      el.style.transform = `translateY(${was - top}px)`
      requestAnimationFrame(() => {
        el.style.transition = 'transform 500ms cubic-bezier(0.2, 0.8, 0.2, 1)'
        el.style.transform = ''
      })
    })
    prev.current = next
  }, [order])
  return ref
}

export function PixelRobot({ id, size = 48, state = 'idle' }: { id: string; size?: number; state?: AgentState }) {
  const sprite = useMemo(() => robotSprite(id), [id])
  const dim = state === 'offline'
  const body = `hsl(${sprite.hue} ${dim ? 10 : 65}% ${dim ? 38 : 58}%)`
  const shade = `hsl(${sprite.hue} ${dim ? 10 : 65}% ${dim ? 26 : 38}%)`
  const eye = state === 'blocked' ? '#fbbf24' : state === 'offline' ? '#52525b' : '#a7f3d0'
  const isEye = (r: number, c: number) => sprite.eyes.some(([er, ec]) => er === r && ec === c)
  return (
    <svg viewBox="0 0 8 8" width={size} height={size} shapeRendering="crispEdges" role="img" aria-label="Robot avatar" className={state === 'working' ? 'agent-bob' : ''}>
      {sprite.pixels.map((row, r) =>
        row.map((on, c) =>
          on && !isEye(r, c) ? <rect key={`${r}-${c}`} x={c} y={r} width={1} height={1} fill={r >= 5 ? shade : body} /> : null,
        ),
      )}
      {sprite.eyes.map(([r, c]) => (
        <rect key={`e${c}`} x={c} y={r} width={1} height={1} fill={eye} className={state === 'offline' ? '' : 'agent-blink'} />
      ))}
    </svg>
  )
}

/** A stepped, 8-bit style bar graph of recent activity; the newest bar pulses while working. */
export function ActivityBars({ series, state, width = 120, height = 32, fluid = false, scaleMax }: { series: number[]; state: AgentState; width?: number; height?: number; fluid?: boolean; scaleMax?: number }) {
  const max = scaleMax ?? Math.max(1, ...series)
  const gap = 1
  const bw = (width - gap * (series.length - 1)) / series.length
  const color = state === 'blocked' ? '#fbbf24' : state === 'offline' ? '#52525b' : '#34d399'
  return (
    <svg
      width={fluid ? '100%' : width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio={fluid ? 'none' : undefined}
      shapeRendering="crispEdges"
      role="img"
      aria-label="Activity over the last 24 hours"
      className="shrink-0"
    >
      {series.map((v, i) => {
        const h = v === 0 ? 1 : Math.max(2, Math.round((v / max) * height))
        const last = i === series.length - 1
        return (
          <rect
            key={i}
            x={i * (bw + gap)}
            y={height - h}
            width={bw}
            height={h}
            fill={color}
            opacity={v === 0 ? 0.25 : 0.45 + 0.55 * (i / series.length)}
            className={last && state === 'working' ? 'agent-pulse-bar' : ''}
          />
        )
      })}
    </svg>
  )
}

function Stat({ label, value, tone }: { label: string; value: string | number; tone?: string }) {
  return (
    <div className="min-w-[3.25rem]">
      <div className={`font-mono text-sm tabular-nums ${tone ?? 'text-zinc-100'}`}>{value}</div>
      <div className="font-mono text-[9px] uppercase tracking-widest text-zinc-500">{label}</div>
    </div>
  )
}

function StateTag({ state }: { state: AgentState }) {
  const s = STATE_STYLE[state]
  return (
    <span className={`inline-flex items-center gap-1.5 font-mono text-[10px] tracking-widest ${s.text}`}>
      <span className={`h-1.5 w-1.5 ${s.dot} ${state === 'working' ? 'agent-pulse-bar' : ''}`} />
      {s.label}
    </span>
  )
}

function Score({ value }: { value: number }) {
  return <span className="font-mono tabular-nums">{String(Math.max(0, value)).padStart(5, '0')}</span>
}

function AgentRow({ agent, now, onOpen }: { agent: Scored; now: number; onOpen: () => void }) {
  const rate = successRate(agent.stats)
  const top = agent.rank === 1 && agent.state !== 'offline'
  return (
    <li data-flip={agent.id} className="border-b border-border">
      <button
        type="button"
        onClick={onOpen}
        aria-label={`Open ${agent.name}`}
        className={`group flex w-full flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 text-left hover:bg-muted/50 focus-visible:bg-muted/50 focus-visible:outline-none ${agent.state === 'offline' ? 'opacity-60' : ''}`}
      >
        <span className={`w-8 shrink-0 text-center font-mono text-xs ${top ? 'text-amber-300' : 'text-zinc-500'}`}>
          {top ? <Crown className="mx-auto h-4 w-4" aria-label="Top of the leaderboard" /> : `#${agent.rank}`}
        </span>
        <span className="shrink-0 border-2 border-zinc-700 bg-zinc-950 p-1 group-hover:border-zinc-500">
          <PixelRobot id={agent.id} size={40} state={agent.state} />
        </span>
        <span className="min-w-0 flex-1 basis-48">
          <span className="flex items-center gap-2">
            <span className="truncate font-mono text-sm font-semibold uppercase tracking-wide">{agent.name}</span>
            {agent.role && <span className="truncate font-mono text-[10px] uppercase tracking-wider text-zinc-500">{agent.role}</span>}
          </span>
          <span className="mt-0.5 block truncate text-[12px] text-muted-foreground">
            {agent.lastAction} · {ago(agent.lastActionAt, now)}
          </span>
          <StateTag state={agent.state} />
        </span>
        <span className="hidden gap-4 sm:flex">
          <Stat label="done" value={agent.stats.done24h} />
          <Stat label="actions" value={agent.stats.actions24h} />
          <Stat label="win%" value={rate === null ? '—' : `${rate}`} tone={rate !== null && rate < 70 ? 'text-amber-300' : undefined} />
          <Stat label="streak" value={agent.stats.streak} />
        </span>
        <ActivityBars series={agent.series} state={agent.state} />
        <span className="w-20 shrink-0 text-right">
          <span className="block text-sm text-amber-200">
            <Score value={agent.score} />
          </span>
          <span className="block font-mono text-[9px] uppercase tracking-widest text-zinc-500">score</span>
        </span>
      </button>
    </li>
  )
}

function AgentModal({ agent, now, onClose }: { agent: Scored; now: number; onClose: () => void }) {
  const rate = successRate(agent.stats)
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <div className="flex items-center gap-4">
            <span className="border-2 border-zinc-600 bg-zinc-950 p-1.5">
              <PixelRobot id={agent.id} size={72} state={agent.state} />
            </span>
            <div className="min-w-0">
              <DialogTitle className="font-mono uppercase tracking-wide">{agent.name}</DialogTitle>
              <DialogDescription className="font-mono text-[11px] uppercase tracking-wider">
                {agent.role ?? 'agent'} · {agent.group ?? 'unassigned'} · rank #{agent.rank}
              </DialogDescription>
              <div className="mt-1">
                <StateTag state={agent.state} />
              </div>
            </div>
            <div className="ml-auto text-right">
              <div className="text-xl text-amber-200">
                <Score value={agent.score} />
              </div>
              <div className="font-mono text-[9px] uppercase tracking-widest text-zinc-500">score</div>
            </div>
          </div>
        </DialogHeader>

        <div className="grid grid-cols-3 gap-3 border-y border-border py-3 sm:grid-cols-6">
          <Stat label="done 24h" value={agent.stats.done24h} />
          <Stat label="failed" value={agent.stats.failed24h} tone={agent.stats.failed24h ? 'text-red-300' : undefined} />
          <Stat label="actions" value={agent.stats.actions24h} />
          <Stat label="win%" value={rate === null ? '—' : rate} />
          <Stat label="streak" value={agent.stats.streak} />
          <Stat label="avg task" value={agent.stats.avgTaskMin === null ? '—' : `${agent.stats.avgTaskMin}m`} />
        </div>

        <section aria-label="Activity, last 24 hours">
          <h3 className="mb-1 font-mono text-[10px] uppercase tracking-widest text-zinc-500">Last 24 hours</h3>
          <ActivityBars series={agent.series} state={agent.state} width={240} height={56} fluid />
        </section>

        <section aria-label="History">
          <h3 className="mb-1 font-mono text-[10px] uppercase tracking-widest text-zinc-500">History</h3>
          <ul className="max-h-56 overflow-auto border border-border">
            {agent.history.length === 0 && <li className="px-3 py-2 text-[13px] text-muted-foreground">No activity yet.</li>}
            {agent.history.map((h, i) => (
              <li key={`${h.at}-${i}`} className="flex items-baseline gap-3 border-b border-border px-3 py-1.5 last:border-b-0">
                <span className={`w-14 shrink-0 font-mono text-[10px] uppercase tracking-wider ${HISTORY_STYLE[h.kind]}`}>{h.kind}</span>
                <span className="min-w-0 flex-1 truncate text-[13px]">{h.text}</span>
                <span className="shrink-0 font-mono text-[10px] text-zinc-500">{ago(h.at, now)}</span>
              </li>
            ))}
          </ul>
        </section>
      </DialogContent>
    </Dialog>
  )
}

export function AgentActivityView({ agents, now, live }: { agents: AgentActivity[]; now: number; live: boolean }) {
  const [openId, setOpenId] = useState<string | null>(null)
  const groups = useMemo(() => groupAgents(agents, now), [agents, now])
  const order = groups.map((g) => `${g.name}:${g.agents.map((a) => a.id).join(',')}`).join('|')
  const ref = useFlip(order)
  const open = groups.flatMap((g) => g.agents).find((a) => a.id === openId) ?? null
  const working = agents.filter((a) => a.state === 'working').length

  return (
    <div ref={ref} className="agent-arcade">
      <div className="flex flex-wrap items-center gap-3 px-4 py-3">
        <p className="text-[13px] text-muted-foreground">Who is doing what, ranked by a leaderboard of finished work, momentum and recency.</p>
        <span className="ml-auto inline-flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-widest text-zinc-400">
          <Zap className="h-3 w-3 text-emerald-400" />
          {working}/{agents.length} working{live ? '' : ' · demo data'}
        </span>
      </div>
      {groups.length === 0 && <p className="px-4 py-6 text-[13px] text-muted-foreground">No agents yet.</p>}
      {groups.map((g) => (
        <section key={g.name} aria-label={g.name} data-flip={`group:${g.name}`}>
          <h2 className="flex items-center gap-3 border-y border-border bg-zinc-950/60 px-4 py-1.5 font-mono text-[11px] uppercase tracking-widest text-zinc-300">
            {g.name}
            <span className="text-zinc-600">{g.agents.length} agent{g.agents.length === 1 ? '' : 's'}</span>
            <span className="ml-auto text-amber-200/80">
              <Score value={g.score} />
            </span>
          </h2>
          <ul aria-label={`${g.name} agents`}>
            {g.agents.map((a) => (
              <AgentRow key={a.id} agent={a} now={now} onOpen={() => setOpenId(a.id)} />
            ))}
          </ul>
        </section>
      ))}
      {open && <AgentModal agent={open} now={now} onClose={() => setOpenId(null)} />}
    </div>
  )
}
