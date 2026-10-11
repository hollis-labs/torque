import type { Suggestion } from '@/lib/types'

// Pure logic for the suggestions UI: confidence bands, grouping, the acceptance
// rate. No React and no network.

export interface Band {
  key: string
  label: string
  /** Inclusive lower bound. */
  min: number
  /** Exclusive upper bound; undefined = no upper bound. */
  max?: number
}

/** Highest first. Bulk accept is offered per band so a person chooses how much to trust at once. */
export const BANDS: Band[] = [
  { key: 'very-high', label: '90% and up', min: 0.9 },
  { key: 'high', label: '80–90%', min: 0.8, max: 0.9 },
  { key: 'medium', label: '70–80%', min: 0.7, max: 0.8 },
  { key: 'low', label: 'below 70%', min: 0, max: 0.7 },
]

export function bandOf(confidence: number): Band {
  return BANDS.find((b) => confidence >= b.min && (b.max === undefined || confidence < b.max)) ?? BANDS[BANDS.length - 1]
}

export interface BandGroup {
  band: Band
  items: Suggestion[]
}

/** Group suggestions into the bands that have any, highest band first. */
export function groupByBand(items: Suggestion[]): BandGroup[] {
  const groups = BANDS.map((band) => ({ band, items: [] as Suggestion[] }))
  for (const s of items) groups.find((g) => g.band === bandOf(s.confidence))!.items.push(s)
  return groups.filter((g) => g.items.length > 0)
}

export function pct(confidence: number): string {
  return `${Math.round(confidence * 100)}%`
}

/** Share of decided suggestions that were accepted, or null when none are decided. */
export function acceptanceRate(counts: Record<string, number> | undefined): number | null {
  if (!counts) return null
  const accepted = (counts.accepted ?? 0) + (counts.auto_applied ?? 0)
  const decided = accepted + (counts.rejected ?? 0)
  return decided === 0 ? null : accepted / decided
}

/** Human label for a question set / key pair. */
export function questionLabel(setId: string, key: string): string {
  return `${setId.replace(/^task-/, '')} · ${key.replace(/_/g, ' ')}`
}
