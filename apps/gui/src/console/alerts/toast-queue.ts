// Decides which alerts raise a toast and holds the visible stack. Framework
// free so it is unit-testable; the host feeds it alerts and prefs.

import { isMutedSource, isQuietNow } from '../prefs/sanitize'
import { MAX_VISIBLE_TOASTS, severityAtLeast } from './policy'
import type { Alert, AlertPrefs } from './types'

export class ToastQueue {
  private seen = new Set<string>()
  private primed = false
  private stack: Alert[] = []
  private listeners = new Set<() => void>()

  /**
   * Offer the current active (unhandled, unread) alerts. The first offer is the
   * backlog and never toasts; later new ids toast if prefs allow.
   */
  offer(alerts: Alert[], prefs: AlertPrefs, now: number = Date.now()): void {
    if (!this.primed) {
      alerts.forEach((a) => this.seen.add(a.id))
      this.primed = true
      return
    }
    const fresh = alerts.filter((a) => !this.seen.has(a.id))
    fresh.forEach((a) => this.seen.add(a.id))
    const live = new Set(alerts.map((a) => a.id))
    let next = this.stack.filter((a) => live.has(a.id)) // handled elsewhere: drop its toast
    // Quiet mode (manual or scheduled quiet hours) and muted sources hold back everything
    // below critical. A critical alert always gets through (owner decision).
    const quiet = isQuietNow(prefs, now)
    const eligible = (a: Alert) =>
      a.severity === 'critical' || (!quiet && !isMutedSource(prefs, a.source) && severityAtLeast(a.severity, prefs.toastMinSeverity))
    next = next.filter(eligible)
    const add = fresh.filter(eligible).sort((a, b) => a.ts - b.ts)
    // A fresh alert replaces a visible toast with the same coalesce key (a burst updates one toast).
    for (const a of add) {
      const i = a.coalesceKey ? next.findIndex((x) => x.coalesceKey === a.coalesceKey) : -1
      if (i === -1) next.push(a)
      else next[i] = a
    }
    // Oldest, lowest severity fall off first; criticals are kept.
    while (next.length > MAX_VISIBLE_TOASTS) {
      const i = next.findIndex((a) => a.severity !== 'critical')
      next.splice(i === -1 ? 0 : i, 1)
    }
    if (next.length !== this.stack.length || next.some((a, i) => a.id !== this.stack[i]?.id)) {
      this.stack = next
      this.emit()
    }
  }

  close(id: string): void {
    const next = this.stack.filter((a) => a.id !== id)
    if (next.length !== this.stack.length) {
      this.stack = next
      this.emit()
    }
  }

  clear(): void {
    if (this.stack.length) {
      this.stack = []
      this.emit()
    }
  }

  getSnapshot = (): Alert[] => this.stack

  subscribe = (l: () => void): (() => void) => {
    this.listeners.add(l)
    return () => this.listeners.delete(l)
  }

  private emit() {
    this.listeners.forEach((l) => l())
  }
}
