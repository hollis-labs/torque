import { refKey } from './detect'
import type { KindResolver, RefEntity, RefState } from './types'

export interface ResolverClientOptions {
  resolvers: Record<string, KindResolver | undefined>
  /** Fresh time for a found entity. Default 60s. */
  ttlMs?: number
  /** Fresh time for not-found (shorter: the thing may be created next). Default 15s. */
  notFoundTtlMs?: number
  /** Wait before retrying after an error. Default 5s. */
  errorRetryMs?: number
  /** Requests landing within this window share one call per kind. Default 10ms. */
  batchWindowMs?: number
  /** Max canonicals per resolveMany call. Default 50. */
  maxBatch?: number
  now?: () => number
}

export interface RefResolverClient {
  /** Current state; never triggers a fetch. Snapshot identity is stable until the state changes. */
  get(kind: string, canonical: string): RefState
  /** Ask for refs to be resolved. Fresh, in-flight and unsupported refs are skipped. */
  request(refs: readonly { kind: string; canonical: string }[]): void
  /** Drop cached state (one ref, one kind, or all) and re-request anything that was resolved. */
  invalidate(kind?: string, canonical?: string): void
  /** useSyncExternalStore-compatible. */
  subscribe(listener: () => void): () => void
  dispose(): void
}

const IDLE: RefState = { status: 'idle' }
const UNSUPPORTED: RefState = { status: 'unsupported' }

/**
 * Cached, batched, deduplicated resolution with explicit loading / not-found /
 * error states. Framework-free: R1 wraps it in a hook with useSyncExternalStore.
 */
export function createRefResolverClient(opts: ResolverClientOptions): RefResolverClient {
  const ttl = opts.ttlMs ?? 60_000
  const notFoundTtl = opts.notFoundTtlMs ?? 15_000
  const errorRetry = opts.errorRetryMs ?? 5_000
  const window = opts.batchWindowMs ?? 10
  const maxBatch = Math.max(1, opts.maxBatch ?? 50)
  const now = opts.now ?? Date.now

  const states = new Map<string, RefState>()
  const queue = new Map<string, { kind: string; canonical: string }>()
  const listeners = new Set<() => void>()
  const aborter = new AbortController()
  let timer: ReturnType<typeof setTimeout> | null = null
  let disposed = false

  const emit = () => listeners.forEach((l) => l())

  function fresh(s: RefState): boolean {
    const t = now()
    switch (s.status) {
      case 'loading':
      case 'unsupported':
        return true
      case 'found':
        return t - s.fetchedAt < ttl
      case 'not-found':
        return t - s.fetchedAt < notFoundTtl
      case 'error':
        return t - s.fetchedAt < errorRetry
      default:
        return false
    }
  }

  function staleOf(s: RefState | undefined): RefEntity | undefined {
    if (!s) return undefined
    return s.status === 'found' ? s.entity : s.status === 'loading' || s.status === 'error' ? s.stale : undefined
  }

  async function runBatch(kind: string, canonicals: string[]) {
    const resolver = opts.resolvers[kind]!
    let result: Awaited<ReturnType<KindResolver['resolveMany']>>
    let failure: string | null = null
    try {
      result = await resolver.resolveMany(canonicals, { signal: aborter.signal })
    } catch (err) {
      if (disposed) return
      result = {}
      failure = err instanceof Error ? err.message : String(err)
    }
    if (disposed) return
    const at = now()
    for (const c of canonicals) {
      const key = refKey(kind, c)
      const prior = states.get(key)
      // invalidate() may have reset this ref while the call was in flight.
      if (prior?.status !== 'loading') continue
      const stale = staleOf(prior)
      if (failure !== null) {
        states.set(key, { status: 'error', message: failure, fetchedAt: at, ...(stale ? { stale } : {}) })
        continue
      }
      const out = result[c]
      states.set(key, out?.status === 'found' ? { status: 'found', entity: out.entity, fetchedAt: at } : { status: 'not-found', fetchedAt: at })
    }
    emit()
  }

  function flush() {
    timer = null
    if (disposed) return
    const byKind = new Map<string, string[]>()
    for (const { kind, canonical } of queue.values()) {
      const list = byKind.get(kind) ?? []
      list.push(canonical)
      byKind.set(kind, list)
    }
    queue.clear()
    for (const [kind, list] of byKind) {
      for (let i = 0; i < list.length; i += maxBatch) void runBatch(kind, list.slice(i, i + maxBatch))
    }
  }

  return {
    get: (kind, canonical) => states.get(refKey(kind, canonical)) ?? IDLE,

    request(refs) {
      if (disposed) return
      let changed = false
      for (const { kind, canonical } of refs) {
        const key = refKey(kind, canonical)
        if (!opts.resolvers[kind]) {
          if (states.get(key)?.status !== 'unsupported') {
            states.set(key, UNSUPPORTED)
            changed = true
          }
          continue
        }
        const prior = states.get(key)
        if (prior && fresh(prior)) continue
        const stale = staleOf(prior)
        states.set(key, { status: 'loading', ...(stale ? { stale } : {}) })
        queue.set(key, { kind, canonical })
        changed = true
      }
      if (queue.size && timer === null) timer = setTimeout(flush, window)
      if (changed) emit()
    },

    invalidate(kind, canonical) {
      const again: { kind: string; canonical: string }[] = []
      for (const [key, s] of [...states]) {
        const [k, c] = key.split('\u0000')
        if ((kind && k !== kind) || (canonical && c !== canonical)) continue
        // Expire in place: the old entity stays visible (as stale) until the new answer lands.
        if (s.status === 'found' || s.status === 'not-found' || s.status === 'error') {
          states.set(key, { ...s, fetchedAt: -Infinity })
          again.push({ kind: k, canonical: c })
        }
      }
      this.request(again)
    },

    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },

    dispose() {
      disposed = true
      if (timer !== null) clearTimeout(timer)
      timer = null
      queue.clear()
      listeners.clear()
      aborter.abort()
    },
  }
}
