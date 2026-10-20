/** Surfaces R2 adopted. Each can be switched off without touching its call site's markup. */
export type AutolinkSurface =
  | 'task-description' | 'task-comments' | 'agents'
  | 'alerts' | 'messaging' | 'forums'

const off = new Set<AutolinkSurface>()

/** Per-surface opt-out: `setAutolinkSurface('messaging', false)`; `?autolink=off` disables all. */
export function setAutolinkSurface(surface: AutolinkSurface, enabled: boolean): void {
  if (enabled) off.delete(surface)
  else off.add(surface)
}

export function isAutolinkSurfaceEnabled(surface: AutolinkSurface): boolean {
  if (off.has(surface)) return false
  try {
    return typeof window === 'undefined' || new URLSearchParams(window.location.search).get('autolink') !== 'off'
  } catch {
    return true
  }
}

