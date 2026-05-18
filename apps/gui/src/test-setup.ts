/**
 * Vitest global setup — runs before every test file. Every guard is a no-op
 * under the `node` environment (where `window` / `Element` are absent), so it
 * is safe for the whole suite regardless of a file's `@vitest-environment`.
 */

// jsdom does not implement `Element.prototype.getAnimations`; base-ui's
// ScrollArea viewport calls it on a timer and would otherwise crash the run.
if (
  typeof Element !== 'undefined' &&
  typeof Element.prototype.getAnimations !== 'function'
) {
  Element.prototype.getAnimations = () => []
}

// vitest's jsdom environment does not reliably expose `localStorage` /
// `sessionStorage` as bare globals — tests (and app code) referencing them
// unqualified then throw. Mirror jsdom's `window` storage onto the global
// scope, falling back to an in-memory Storage when jsdom omits it entirely.
function memoryStorage(): Storage {
  const map = new Map<string, string>()
  return {
    get length() {
      return map.size
    },
    clear: () => map.clear(),
    getItem: (key: string) => (map.has(key) ? map.get(key)! : null),
    key: (index: number) => Array.from(map.keys())[index] ?? null,
    removeItem: (key: string) => {
      map.delete(key)
    },
    setItem: (key: string, value: string) => {
      map.set(key, String(value))
    },
  } as Storage
}

if (typeof window !== 'undefined') {
  for (const name of ['localStorage', 'sessionStorage'] as const) {
    let store: Storage | undefined
    try {
      store = window[name]
    } catch {
      store = undefined
    }
    if (!store) {
      store = memoryStorage()
      Object.defineProperty(window, name, { value: store, configurable: true })
    }
    if (typeof (globalThis as Record<string, unknown>)[name] === 'undefined') {
      ;(globalThis as Record<string, unknown>)[name] = store
    }
  }
}
