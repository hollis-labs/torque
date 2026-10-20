import { stateSegments, type FleetTransition } from '@/lib/fleet-agents'
import { FLEET_STATE_STYLE } from '@/components/agents/fleet-style'

/**
 * A stepped strip of the states this page has observed over a window. The
 * lead-in before the page started watching is left empty: the source keeps no
 * history, so it is not painted as anything.
 */
export function StateTimeline({ entries, now, windowMs, width = 120, height = 14, fluid = false }: { entries: FleetTransition[]; now: number; windowMs: number; width?: number; height?: number; fluid?: boolean }) {
  const segs = stateSegments(entries, now, windowMs)
  const start = now - windowMs
  const x = (t: number) => ((t - start) / windowMs) * width
  const minutes = Math.round(windowMs / 60_000)
  return (
    <svg
      width={fluid ? '100%' : width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio={fluid ? 'none' : undefined}
      shapeRendering="crispEdges"
      role="img"
      aria-label={`Observed state over the last ${minutes} minutes`}
      className="shrink-0 bg-zinc-900/60"
    >
      {segs.map((g, i) =>
        g.state ? <rect key={i} x={x(g.from)} y={0} width={Math.max(1, x(g.to) - x(g.from))} height={height} fill={FLEET_STATE_STYLE[g.state].fill} opacity={0.85} /> : null,
      )}
    </svg>
  )
}
