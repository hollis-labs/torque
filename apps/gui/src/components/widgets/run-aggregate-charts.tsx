import { Area, AreaChart, Bar, BarChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { BarMeter, DonutChart, type BarMeterRow } from '@hollis-labs/sysop-ui/widgets'
import type { AggregateFacet, RunTimeBucket, RunTimeSeries } from '@/lib/api'
import { STATUS_COLOR_VAR, STATUS_LABEL, TASK_STATUSES } from '@/lib/constants'

// Plot the server window directly; the kit chart rebuilds a browser-local
// last-N-days window, which can discard a boundary bucket after midnight.
function BucketChart({ items, series, title, height, kind = 'bar', formatValue = String }: {
  items: RunTimeBucket[]
  title: string
  height: number
  kind?: 'bar' | 'area'
  formatValue?: (value: number) => string
  series: { key: string; label: string; color: string; value: (bucket: RunTimeBucket) => number }[]
}) {
  const data = items.map((bucket) => Object.fromEntries([['day', bucket.start.slice(5, 10)], ...series.map((entry) => [entry.key, entry.value(bucket)])]))
  const tick = { fill: 'currentColor', fontSize: 9, fontFamily: 'ui-monospace' }
  const axes = <><XAxis dataKey="day" tick={tick} axisLine={false} tickLine={false} /><YAxis width={58} tick={tick} axisLine={false} tickLine={false} allowDecimals={false} tickFormatter={formatValue} /><Tooltip formatter={(value) => formatValue(Number(value))} contentStyle={{ background: 'var(--color-popover)', color: 'var(--color-popover-foreground)', border: '1px solid var(--color-border)', fontFamily: 'ui-monospace', fontSize: 11 }} /></>
  return <div className="flex flex-col gap-2 text-muted-foreground" aria-label={title}>
    <div className="flex items-center justify-between font-mono text-[9px] uppercase tracking-[0.2em]"><span>{title} — last 14d</span><div className="flex gap-3">{series.map((entry) => <span key={entry.key} className="inline-flex items-center gap-1"><span className="h-2 w-2 rounded-sm" style={{ backgroundColor: entry.color }} />{entry.label}</span>)}</div></div>
    <ResponsiveContainer width="100%" height={height}>{kind === 'area' ? <AreaChart data={data} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>{axes}{series.map((entry) => <Area key={entry.key} dataKey={entry.key} name={entry.label} type="monotone" stroke={entry.color} fill={entry.color} fillOpacity={0.2} />)}</AreaChart> : <BarChart data={data} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>{axes}{series.map((entry) => <Bar key={entry.key} dataKey={entry.key} name={entry.label} stackId="series" fill={entry.color} />)}</BarChart>}</ResponsiveContainer>
  </div>
}

function formatTokens(value: number) {
  return value.toLocaleString()
}

function runKind(status: string): 'success' | 'error' | 'active' {
  if (['done', 'completed', 'success'].includes(status)) return 'success'
  if (['failed', 'error', 'cancelled', 'canceled', 'timeout'].includes(status)) return 'error'
  return 'active'
}

export function AggregateRunHistory({ series }: { series: RunTimeSeries }) {
  return <BucketChart items={series.buckets} title="Runs / day · UTC" height={160} series={[
    { key: 'success', label: 'success', color: STATUS_COLOR_VAR.done, value: (b) => Object.entries(b.status_counts).reduce((n, [s, count]) => n + (runKind(s) === 'success' ? count : 0), 0) },
    { key: 'error', label: 'error', color: STATUS_COLOR_VAR.blocked, value: (b) => Object.entries(b.status_counts).reduce((n, [s, count]) => n + (runKind(s) === 'error' ? count : 0), 0) },
    { key: 'active', label: 'active', color: STATUS_COLOR_VAR.doing, value: (b) => Object.entries(b.status_counts).reduce((n, [s, count]) => n + (runKind(s) === 'active' ? count : 0), 0) },
  ]} />
}

export function AggregateTokens({ series }: { series: RunTimeSeries }) {
  return <div className="flex flex-col gap-2">
    <BucketChart items={series.buckets} title="Tokens / day · UTC" height={160} formatValue={formatTokens} series={[
      { key: 'prompt', label: 'prompt', color: STATUS_COLOR_VAR.doing, value: (b) => b.prompt_tokens },
      { key: 'completion', label: 'completion', color: STATUS_COLOR_VAR.done, value: (b) => b.completion_tokens },
    ]} />
    <span className="font-mono text-[10px] text-muted-foreground">{formatTokens(series.totals.prompt_tokens + series.totals.completion_tokens)} total tokens</span>
  </div>
}

export function AggregateCost({ series }: { series: RunTimeSeries }) {
  return <div className="flex flex-col gap-2">
    <span className="text-right font-mono text-[10px] text-muted-foreground">${series.totals.cost.toFixed(2)} total</span>
    <BucketChart items={series.buckets} title="Cost / day · UTC" height={140} kind="area" formatValue={(v) => `$${v.toFixed(2)}`} series={[
      { key: 'cost', label: 'cost', color: STATUS_COLOR_VAR.review, value: (b) => b.cost },
    ]} />
  </div>
}

export function AggregateTaskPipeline({ facet, total }: { facet?: AggregateFacet; total: number }) {
  const counts = new Map(facet?.buckets.map((b) => [String(b.value), b.count]))
  const rows: BarMeterRow[] = TASK_STATUSES.map((s) => ({ key: s, label: STATUS_LABEL[s], value: counts.get(s) ?? 0, color: STATUS_COLOR_VAR[s] }))
  const other = total - rows.reduce((n, r) => n + r.value, 0)
  if (other > 0) rows.push({ key: 'other', label: 'Other', value: other, color: STATUS_COLOR_VAR.backlog })
  return <BarMeter rows={rows} title="Task Pipeline" />
}

export function AggregateRunStatus({ facet, total }: { facet?: AggregateFacet; total: number }) {
  const counts = { success: 0, error: 0, active: 0 }
  for (const b of facet?.buckets ?? []) counts[runKind(String(b.value))] += b.count
  const other = total - Object.values(counts).reduce((n, v) => n + v, 0)
  return <DonutChart title="Run status — 14d" centerLabel="runs" size={120} segments={[
    { key: 'success', label: 'success', value: counts.success, color: STATUS_COLOR_VAR.done },
    { key: 'error', label: 'error', value: counts.error, color: STATUS_COLOR_VAR.blocked },
    { key: 'active', label: 'active', value: counts.active, color: STATUS_COLOR_VAR.doing },
    { key: 'other', label: 'other', value: Math.max(0, other), color: STATUS_COLOR_VAR.backlog },
  ]} />
}

export function AggregateBreakdown({ facet, total, title }: { facet?: AggregateFacet; total: number; title: string }) {
  const rows = (facet?.buckets ?? []).map((b, i) => ({ key: `${i}`, label: String(b.value || 'Default'), value: b.count, color: STATUS_COLOR_VAR.doing }))
  const other = total - rows.reduce((n, r) => n + r.value, 0)
  if (other > 0) rows.push({ key: 'other', label: 'Other', value: other, color: STATUS_COLOR_VAR.backlog })
  return <BarMeter rows={rows} title={title} />
}

// Kit heatmap/pulse APIs count individual items. Render weighted buckets
// directly so an aggregate never expands back into one item per run.
export function AggregateRunHeatmap({ series }: { series: RunTimeSeries }) {
  const max = Math.max(1, ...series.buckets.map((b) => b.count))
  return <div aria-label="Run activity heatmap" className="flex flex-col gap-2">
    <span className="font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground">Run activity — 16w · UTC</span>
    <div className="grid grid-flow-col grid-rows-7 gap-[3px] self-start" role="img" aria-label={`${series.totals.count} runs over 16 weeks`}>
      {series.buckets.map((b) => <div key={b.start} title={`${b.start.slice(0, 10)}: ${b.count} runs`} className="h-[10px] w-[10px] rounded-sm bg-muted/40" style={b.count ? { backgroundColor: STATUS_COLOR_VAR.done, opacity: 0.2 + 0.8 * b.count / max } : undefined} />)}
    </div>
    <span className="font-mono text-[9px] text-muted-foreground">{series.totals.count.toLocaleString()} total runs</span>
  </div>
}

export function AggregateRunPulse({ series }: { series: RunTimeSeries }) {
  const max = Math.max(1, ...series.buckets.map((b) => b.count))
  return <div aria-label="24h run pulse" className="flex flex-col gap-2">
    <div className="flex items-center justify-between font-mono text-[9px] text-muted-foreground"><span>24h pulse · UTC</span><span>{series.totals.count.toLocaleString()} runs · 24h</span></div>
    <div className="flex h-12 items-end gap-[2px]" role="img" aria-label={`${series.totals.count} runs in last 24 hours`}>
      {series.buckets.map((b) => <div key={b.start} title={`${b.start.slice(0, 16)} UTC — ${b.count} runs`} className="flex h-full flex-1 flex-col justify-end rounded-sm bg-muted/40"><div className="w-full rounded-sm" style={{ height: `${b.count ? Math.max(8, 100 * b.count / max) : 0}%`, backgroundColor: STATUS_COLOR_VAR.done }} /></div>)}
    </div>
    <div className="flex justify-between font-mono text-[8px] text-muted-foreground"><span>-24h</span><span>-12h</span><span>now</span></div>
  </div>
}
