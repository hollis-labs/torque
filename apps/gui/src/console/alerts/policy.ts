import { SEVERITY_ORDER, type ConsoleSeverity } from '../events/types'

/** Auto-expire per severity, ms. null = sticky until dismissed. */
export const TOAST_EXPIRE_MS: Record<ConsoleSeverity, number | null> = {
  debug: 3000,
  info: 5000,
  notice: 7000,
  warning: 12000,
  error: 20000,
  critical: null,
}

export const MAX_VISIBLE_TOASTS = 4

export function severityAtLeast(s: ConsoleSeverity, min: ConsoleSeverity): boolean {
  return SEVERITY_ORDER.indexOf(s) >= SEVERITY_ORDER.indexOf(min)
}

/** Only same-origin paths and http(s) URLs are ever rendered as links (event text is untrusted). */
export function safeHref(href: string | undefined): string | null {
  if (!href) return null
  if (href.startsWith('/') && !href.startsWith('//')) return href
  try {
    const u = new URL(href)
    return u.protocol === 'https:' || u.protocol === 'http:' ? href : null
  } catch {
    return null
  }
}

export function isInternalHref(href: string): boolean {
  return href.startsWith('/')
}
