// Live views on real data: scope progress/burndown (Torque) and fleet metrics (event stream).
import { useMemo, useState } from 'react'
import { useConsoleEvents } from '../events/hooks'
import { KINDS } from '../events/types'
import { Burndown } from './Burndown'
import { fleetMetricsFromEvents, FLEET_STATES } from './fleet'
import { LABEL, Placeholder } from './frame'
import { ProgressBar } from './ProgressBar'
import { StatTile } from './StatTile'
import type { MetricsSource } from './types'
import { useMetricsSource } from './use-metrics-source'
import { useMetrics } from './useMetrics'

export interface ScopeMetricsPanelProps {
  /** "epic:<id>" | "sprint:<id>" | "project:<id>" | "tag:<slug>" */
  scope: string
  title?: string
  /** compact: progress + tiles only (card/inline use). */
  compact?: boolean
  now?: number
  source?: MetricsSource
}

const TILE_GRID = 'grid grid-cols-2 border-t border-l border-zinc-800/80 md:grid-cols-4 [&>*]:border-b [&>*]:border-r [&>*]:border-zinc-800/80'

export function ScopeMetricsPanel({ scope, title, compact, now, source }: ScopeMetricsPanelProps) {
  const fromCtx = useMetricsSource()
  const m = useMetrics(source ?? fromCtx, scope)
  const loading = m.status === 'loading'
  const data = m.status === 'ready' ? m.data : null
  const heading = title ?? scope
  return (
    <section aria-label={`Progress metrics for ${heading}`} data-metrics-scope={scope} className="border border-zinc-800">
      <header className="flex items-center gap-3 border-b border-zinc-800 px-4 py-2">
        <h3 className={LABEL}>{heading}</h3>
        {data?.origin === 'fixture' && <span className="border border-amber-400/60 px-1.5 py-px font-mono text-[10px] uppercase tracking-widest text-amber-300">fixture</span>}
        {data?.burnQuality === 'derived' && <span className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">burn approximate</span>}
      </header>
      <div className="space-y-4 p-4">
        {m.status === 'error' && <Placeholder kind="error" label={heading} message={`Metrics unavailable: ${m.message}`} />}
        {m.status !== 'error' && (
          <>
            {!compact && (
              <div className={TILE_GRID}>
                {(data?.tiles ?? [null, null, null, null]).map((t, i) => (
                  <StatTile key={t?.id ?? i} label={t?.label ?? ''} value={t?.value} unit={t?.unit} previous={t?.previous} invert={t?.invert} series={t?.series} loading={loading} />
                ))}
              </div>
            )}
            <ProgressBar segments={data?.progress ?? []} loading={loading} startTs={data?.burn.startTs} now={now} label="Progress" />
            {!compact && (data?.burnQuality === 'unavailable' ? (
              <Placeholder kind="empty" label="Burndown" message="No live tasks in this scope; nothing to chart" height={72} />
            ) : (
              <div className="grid gap-4 lg:grid-cols-2">
                <Burndown data={data?.burn} loading={loading} now={now} />
                <Burndown data={data?.burn} loading={loading} now={now} mode="burnup" />
              </div>
            ))}
            {data?.notes && data.notes.length > 0 && (
              <ul className="space-y-0.5 font-mono text-[11px] text-zinc-500">{data.notes.map((n) => <li key={n}>{n}</li>)}</ul>
            )}
          </>
        )}
      </div>
    </section>
  )
}

export interface FleetMetricsPanelProps {
  windowMs?: number
  now?: number
  title?: string
}

/** Working / idle / waiting counts over time plus tokens and cost when the stream carries them. Needs <ConsoleProvider>. */
export function FleetMetricsPanel({ windowMs, now, title = 'Fleet' }: FleetMetricsPanelProps) {
  const events = useConsoleEvents({ kinds: [KINDS.agentState, KINDS.metricTick], hideHandled: false, newestFirst: false })
  const [mounted] = useState(() => Date.now())
  const m = useMemo(() => fleetMetricsFromEvents(events, { now: now ?? Math.max(mounted, ...events.map((e) => e.ts)), windowMs }), [events, now, mounted, windowMs])
  return (
    <section aria-label="Fleet metrics" className="border border-zinc-800">
      <header className="flex items-center gap-3 border-b border-zinc-800 px-4 py-2">
        <h3 className={LABEL}>{title}</h3>
        <span className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">{m.total} agent{m.total === 1 ? '' : 's'}</span>
      </header>
      <div className="space-y-3 p-4">
        {!m.hasData ? (
          <Placeholder kind="empty" label="Fleet" message="No agent activity in this window" height={72} />
        ) : (
          <div className={TILE_GRID}>
            {FLEET_STATES.map((k) => (
              <StatTile key={k} label={k} value={m.counts[k]} series={m.series[k]} invert={k === 'waiting'} />
            ))}
            {m.tokens && <StatTile label="tokens / min" value={m.tokens.points[m.tokens.points.length - 1].v} series={m.tokens} />}
            {m.cost && <StatTile label="cost" value={m.cost.points[m.cost.points.length - 1].v} series={m.cost} invert />}
          </div>
        )}
        {m.hasData && m.notes.length > 0 && <ul className="space-y-0.5 font-mono text-[11px] text-zinc-500">{m.notes.map((n) => <li key={n}>{n}</li>)}</ul>}
      </div>
    </section>
  )
}
