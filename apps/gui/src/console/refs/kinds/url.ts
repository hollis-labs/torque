import type { RefKind } from '../types'
import { isWordChar, matches, trimTrailing } from '../scan-utils'

const URL_RE = /https?:\/\/[^\s<>"`]{1,2048}/g

export function createUrlKind(): RefKind {
  return {
    id: 'url',
    label: 'Link',
    icon: 'link',
    priority: 100,
    scan(text) {
      const hits = []
      for (const m of matches(URL_RE, text)) {
        const start = m.index
        if (isWordChar(text[start - 1])) continue
        const end = trimTrailing(text, start, start + m[0].length)
        const raw = text.slice(start, end)
        let u: URL
        try {
          u = new URL(raw)
        } catch {
          continue
        }
        const host = u.hostname
        if (!host || !(host.includes('.') || host === 'localhost' || host.includes(':'))) continue
        hits.push({ start, end, canonical: raw, meta: { host } })
      }
      return hits
    },
    chipLabel: (ref) => {
      try {
        const u = new URL(ref.canonical)
        const path = u.pathname === '/' ? '' : u.pathname
        const s = `${u.host}${path}`
        return s.length > 48 ? `${s.slice(0, 47)}…` : s
      } catch {
        return ref.canonical
      }
    },
    open: (ref) => ({ type: 'external', href: ref.canonical }),
  }
}
