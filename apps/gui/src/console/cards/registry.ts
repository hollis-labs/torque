// CardKind registry: kind id -> renderers per density + the action schema the
// kind understands. Unknown kinds fall back to a generic renderer, never throw.
import { createElement, type ComponentType, type ReactElement } from 'react'
import type { CardActionSpec, CardDensity, CardKindId, CardRenderProps, CardSurface } from './types'

export interface CardKindDef {
  id: CardKindId
  label: string
  /** Renderer per density. `expanded` is required; compact/inline fall back to it. */
  render: Partial<Record<CardDensity, ComponentType<CardRenderProps>>> & { expanded: ComponentType<CardRenderProps> }
  /** Action ids this kind knows how to present, with defaults used when a rule/event offers none. */
  actionSchema?: { id: string; label: string; intent: CardActionSpec['intent']; kind: string }[]
  /** Surfaces this kind may appear on; rules asking for others are filtered out. Default: all. */
  surfaces?: CardSurface[]
  /** Tray/toast sort weight, higher first within equal severity. */
  priority?: number
}

export interface CardRegistry {
  register(def: CardKindDef): () => void
  get(id: CardKindId): CardKindDef | undefined
  list(): CardKindDef[]
  /** Resolves the component for a kind+density, falling back through density then to the generic kind. */
  resolve(id: CardKindId, density: CardDensity): ComponentType<CardRenderProps>
  /** Renders the card with the resolved component. */
  render(props: CardRenderProps): ReactElement
}

export function createCardRegistry(fallback: CardKindDef, initial: CardKindDef[] = []): CardRegistry {
  const defs = new Map<CardKindId, CardKindDef>()
  const reg: CardRegistry = {
    register(def) {
      defs.set(def.id, def)
      return () => {
        if (defs.get(def.id) === def) defs.delete(def.id)
      }
    },
    get: (id) => defs.get(id),
    list: () => [...defs.values()],
    resolve(id, density) {
      const d = defs.get(id) ?? fallback
      return d.render[density] ?? d.render.expanded
    },
    render(props) {
      return createElement(reg.resolve(props.card.kind, props.density), props)
    },
  }
  initial.forEach(reg.register)
  return reg
}
