import type { RefRegistry } from './registry'
import type { PreviewCard, RefIdentity, RefState } from './types'

/**
 * The hovercard descriptor for a reference in any state. Loading and
 * not-found cards still carry the kind, id and open target, so the card is
 * useful before (or without) the backend.
 */
export function buildPreviewCard(registry: RefRegistry, ref: RefIdentity, state: RefState): PreviewCard {
  const kind = registry.get(ref.kind)
  const base = {
    kind: ref.kind,
    kindLabel: kind?.label ?? ref.kind,
    icon: kind?.icon ?? 'link',
    canonical: ref.canonical,
    fields: [] as PreviewCard['fields'],
    open: kind ? kind.open(ref) : ({ type: 'none' } as const),
  }
  const title = kind?.chipLabel?.(ref, ref.canonical) ?? ref.canonical
  switch (state.status) {
    case 'found':
      return { ...base, phase: 'ready', title: state.entity.title, subtitle: state.entity.subtitle, description: state.entity.description, status: state.entity.status, fields: state.entity.fields ?? [], updatedAt: state.entity.updatedAt }
    case 'loading':
      return state.stale
        ? { ...base, phase: 'loading', title: state.stale.title, subtitle: state.stale.subtitle, status: state.stale.status, fields: state.stale.fields ?? [] }
        : { ...base, phase: 'loading', title }
    case 'not-found':
      return { ...base, phase: 'not-found', title, subtitle: `${base.kindLabel} not found` }
    case 'error':
      return { ...base, phase: 'error', title: state.stale?.title ?? title, error: state.message, fields: state.stale?.fields ?? [] }
    case 'unsupported':
      return { ...base, phase: 'unresolved', title }
    default:
      return { ...base, phase: 'loading', title }
  }
}
