import { sanitizePrefs } from './sanitize'
import { DEFAULT_CONSOLE_PREFS, type ConsolePrefs, type ConsolePrefsStore } from './types'

export class MemoryConsolePrefsStore implements ConsolePrefsStore {
  protected value: ConsolePrefs
  private listeners = new Set<() => void>()
  constructor(initial?: Partial<ConsolePrefs>) {
    this.value = sanitizePrefs({ ...DEFAULT_CONSOLE_PREFS, ...initial })
  }
  // Arrow properties: passed unbound to useSyncExternalStore.
  get = (): ConsolePrefs => this.value
  set(patch: Partial<ConsolePrefs>): void {
    this.value = sanitizePrefs({ ...this.value, ...patch })
    this.persist()
    this.listeners.forEach((l) => l())
  }
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }
  protected persist(): void {}
}

/** Same key A1 used, so existing {quiet, toastMinSeverity} entries keep working (the new fields default). */
export const CONSOLE_PREFS_STORAGE_KEY = 'console.alerts.prefs.v1'

/** Per-device mock of the future server-backed store. */
export class LocalConsolePrefsStore extends MemoryConsolePrefsStore {
  private readonly key: string
  constructor(key = CONSOLE_PREFS_STORAGE_KEY) {
    super()
    this.key = key
    try {
      const raw = window.localStorage.getItem(key)
      if (raw) this.value = sanitizePrefs(JSON.parse(raw))
    } catch {
      /* unavailable or corrupt: defaults */
    }
  }
  protected persist(): void {
    try {
      window.localStorage.setItem(this.key, JSON.stringify(this.value))
    } catch {
      /* private mode / quota: keep in memory */
    }
  }
}
