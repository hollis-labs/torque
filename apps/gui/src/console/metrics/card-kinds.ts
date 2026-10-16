// Metric card kind ids, registry defs, and placeholder rules (see cards.tsx for the renderers).
import type { CardKindDef, CardRegistry } from '../cards/registry'
import type { CardRuleSet } from '../cards/rules'
import { BurndownMetricCard, FleetMetricCard, ProgressMetricCard } from './cards'

export const METRIC_CARD_KINDS = { progress: 'metric.progress', burndown: 'metric.burndown', fleet: 'metric.fleet' } as const

export const METRIC_CARD_DEFS: CardKindDef[] = [
  { id: METRIC_CARD_KINDS.progress, label: 'Progress', render: { expanded: ProgressMetricCard }, surfaces: ['tray', 'inline', 'modal'], priority: 0 },
  { id: METRIC_CARD_KINDS.burndown, label: 'Burndown', render: { expanded: BurndownMetricCard }, surfaces: ['tray', 'inline', 'modal'], priority: 0 },
  { id: METRIC_CARD_KINDS.fleet, label: 'Fleet', render: { expanded: FleetMetricCard }, surfaces: ['tray', 'inline', 'modal'], priority: 0 },
]

/** Registers the metric kinds; returns one disposer. */
export function registerMetricCardKinds(registry: CardRegistry): () => void {
  const offs = METRIC_CARD_DEFS.map((d) => registry.register(d))
  return () => offs.forEach((o) => o())
}

/** Placeholder event kinds -> metric cards. Merge into the host's CardRuleSet or replace once the stream design lands. */
export const METRIC_CARD_RULES: CardRuleSet = {
  rules: Object.values(METRIC_CARD_KINDS).map((kind) => ({
    id: kind,
    match: { kinds: [kind] },
    card: {
      kind,
      surfaces: ['tray', 'inline'] as ('tray' | 'inline')[],
      dedupeKey: `${kind}:{payload.scope|id}`,
      title: '{payload.title|payload.scope}',
      severity: 'info' as const,
      actionsFrom: 'declared' as const,
    },
  })),
}
