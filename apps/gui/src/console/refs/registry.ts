import type { RefKind, RefKindId } from './types'

const ID_RE = /^[a-z][a-z0-9]*(\.[a-z0-9]+)*$/

export interface RefRegistry {
  /**
   * Add a kind. Throws on a malformed or duplicate id (pass `replace: true`
   * to swap a kind, e.g. a live scanner for a stub). Returns an unregister
   * function so a feature can clean up after itself.
   */
  register(kind: RefKind, opts?: { replace?: boolean }): () => void
  unregister(id: RefKindId): boolean
  get(id: RefKindId): RefKind | undefined
  has(id: RefKindId): boolean
  /** Kinds in scan order: priority descending, then registration order. */
  list(): RefKind[]
  /** Bumps on every change, so memoised detectors can invalidate. */
  readonly version: number
}

export function createRefRegistry(): RefRegistry {
  const kinds = new Map<RefKindId, RefKind>()
  let version = 0

  const registry: RefRegistry = {
    register(kind, opts) {
      if (!ID_RE.test(kind.id)) throw new Error(`invalid ref kind id: ${JSON.stringify(kind.id)}`)
      if (!Number.isFinite(kind.priority)) throw new Error(`ref kind ${kind.id}: priority must be a finite number`)
      if (kinds.has(kind.id) && !opts?.replace) throw new Error(`ref kind already registered: ${kind.id}`)
      kinds.set(kind.id, kind)
      version++
      return () => {
        // Only remove our own registration, not a replacement.
        if (kinds.get(kind.id) === kind) registry.unregister(kind.id)
      }
    },
    unregister(id) {
      const had = kinds.delete(id)
      if (had) version++
      return had
    },
    get: (id) => kinds.get(id),
    has: (id) => kinds.has(id),
    list: () => [...kinds.values()].sort((a, b) => b.priority - a.priority),
    get version() {
      return version
    },
  }
  return registry
}
