import { useMemo } from 'react'
import { Burndown } from './Burndown'
import { ProgressBar } from './ProgressBar'
import { Sparkline } from './Sparkline'
import { StackedBar } from './StackedBar'
import { StatTile } from './StatTile'
import { fixtureProgress, fixtureSeries, FixtureMetricsSource } from './fixtures'
import { useMetrics } from './useMetrics'

const NOW = Date.parse('2026-10-16T12:00:00Z')
const H = 'font-mono text-[10px] uppercase tracking-widest text-zinc-500'

/** Showcase for WidgetPreviewPage: live fixture plus the degenerate states. */
export function MetricsPreview() {
  const source = useMemo(() => new FixtureMetricsSource(), [])
  const m = useMetrics(source)
  const ready = m.status === 'ready' ? m.data : null
  const loading = m.status === 'loading'
  return (
    <div className="space-y-6">
      {m.status === 'error' && <p role="alert" className="font-mono text-xs text-rose-300">{m.message}</p>}
      <div className="grid grid-cols-2 border-t border-zinc-800/80 md:grid-cols-4">
        {(ready?.tiles ?? [null, null, null, null]).map((t, i) => (
          <StatTile key={t?.id ?? i} label={t?.label ?? ''} value={t?.value} unit={t?.unit} previous={t?.previous} invert={t?.invert} series={t?.series} loading={loading} />
        ))}
      </div>
      <ProgressBar segments={ready?.progress ?? []} loading={loading} startTs={ready?.burn.startTs} now={NOW} label="Epic progress" />
      <div className="grid gap-6 lg:grid-cols-2">
        <Burndown data={ready?.burn} loading={loading} now={NOW} />
        <Burndown data={ready?.burn} loading={loading} now={NOW} mode="burnup" />
      </div>
      <StackedBar segments={ready?.breakdown ?? []} loading={loading} label="Runs by outcome" />
      <div>
        <h3 className={`mb-2 ${H}`}>Edge states</h3>
        <div className="grid gap-4 md:grid-cols-3">
          <ProgressBar segments={fixtureProgress('empty')} label="Zero tasks" />
          <ProgressBar segments={fixtureProgress('done')} label="All done" />
          <ProgressBar segments={fixtureProgress('single')} label="One task" />
          <div className="flex items-center gap-3"><span className={H}>Single point</span><Sparkline label="Single point" points={[{ t: 1, v: 5 }]} /></div>
          <div className="flex items-center gap-3"><span className={H}>No data</span><Sparkline label="No data" points={[]} /></div>
          <div className="flex items-center gap-3"><span className={H}>Rising</span><Sparkline label="Rising" tone="good" points={fixtureSeries('r', { drift: 2, noise: 4, n: 20 }).points} /></div>
        </div>
      </div>
    </div>
  )
}
