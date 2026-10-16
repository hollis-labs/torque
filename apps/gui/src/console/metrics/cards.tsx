// Card kinds (K1) that embed a live metric. Rules are data and swappable: events whose
// payload carries {scope} render a live Torque-backed panel instead of a static snapshot.
import { CardFrame } from '../cards/frame'
import type { CardRenderProps } from '../cards/types'
import { FleetMetricsPanel, ScopeMetricsPanel } from './panels'

function scopeOf(p: CardRenderProps): string {
  const fromProps = p.card.props['scope']
  const fromPayload = (p.card.event.payload as { scope?: unknown }).scope
  const s = typeof fromProps === 'string' ? fromProps : typeof fromPayload === 'string' ? fromPayload : ''
  return s
}

function ScopeCard(p: CardRenderProps & { variant: 'progress' | 'burndown' }) {
  const scope = scopeOf(p)
  return (
    <CardFrame card={p.card} density={p.density} kindLabel={p.variant === 'progress' ? 'Progress' : 'Burndown'} onExpand={p.onExpand}>
      <div className="mt-2">
        {scope ? <ScopeMetricsPanel scope={scope} title={p.card.title} compact={p.variant === 'progress' || p.density === 'compact'} /> : <p className="font-mono text-[11px] text-zinc-500">No scope on this event.</p>}
      </div>
    </CardFrame>
  )
}

export function ProgressMetricCard(p: CardRenderProps) { return <ScopeCard {...p} variant="progress" /> }
export function BurndownMetricCard(p: CardRenderProps) { return <ScopeCard {...p} variant="burndown" /> }
export const FleetMetricCard = (p: CardRenderProps) => (
  <CardFrame card={p.card} density={p.density} kindLabel="Fleet" onExpand={p.onExpand}>
    <div className="mt-2"><FleetMetricsPanel title={p.card.title} /></div>
  </CardFrame>
)

