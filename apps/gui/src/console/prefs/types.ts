// ConsolePrefs: the user's Console preferences as one typed, flat value object.
// Server-backed later (see BACKEND_NEEDS.md); a memory/localStorage mock today.
// Field names match A1's AlertPrefs (quiet, toastMinSeverity), so the existing
// localStorage entry stays readable.

import type { ConsoleSeverity } from '../events/types'

export interface ConsolePrefs {
  /** Quiet mode: no toasts, EXCEPT critical alerts, which always get through. The tray and badge keep counting. */
  quiet: boolean
  /** Lowest severity that raises a toast. Everything still lands in the tray. */
  toastMinSeverity: ConsoleSeverity
  /** Scheduled quiet mode, evaluated in the viewer's local time. */
  quietHoursEnabled: boolean
  /** "HH:MM" 24h. A window that wraps midnight (22:00 -> 07:00) is supported. */
  quietHoursStart: string
  quietHoursEnd: string
  /** Case-insensitive substrings of an alert's source (agent / machine). Muted sources raise no toast; critical still does. */
  mutedSources: string[]
  /** Same-key alerts closer together than this fold into one entry. 0 disables coalescing. */
  coalesceWindowSec: number
}

export const DEFAULT_CONSOLE_PREFS: ConsolePrefs = {
  quiet: false,
  toastMinSeverity: 'notice',
  quietHoursEnabled: false,
  quietHoursStart: '22:00',
  quietHoursEnd: '07:00',
  mutedSources: [],
  coalesceWindowSec: 120,
}

/**
 * Where prefs live. Synchronous reads (useSyncExternalStore friendly) with an
 * optional async `load` for a server-backed implementation: it fetches, replaces
 * the cached value and notifies subscribers. `set` is optimistic locally and a
 * server store persists in the background (it must not report a saved state it
 * has not confirmed; see the kit-settings README).
 */
export interface ConsolePrefsStore {
  get(): ConsolePrefs
  set(patch: Partial<ConsolePrefs>): void
  subscribe(listener: () => void): () => void
  /** Server-backed stores: (re)read from the server. Local stores omit it. */
  load?(): Promise<void>
}
