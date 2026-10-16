import type { RefKind } from '../types'
import { isWordChar, matches, trimTrailing } from '../scan-utils'

// `ws-agent-console` (the workstream tag) or explicit `workstream:name`.
const TAG = /ws-[a-z][a-z0-9-]{2,48}/g
const EXPLICIT = /workstream:([A-Za-z0-9][A-Za-z0-9._-]{0,63})/g

export function createWorkstreamKind(): RefKind {
  return {
    id: 'workstream',
    label: 'Workstream',
    icon: 'workstream',
    priority: 60,
    scan(text) {
      const hits = []
      for (const m of matches(TAG, text)) {
        const start = m.index
        const before = text[start - 1]
        if (isWordChar(before) || before === '-' || before === '/' || before === '.') continue
        const end = trimTrailing(text, start, start + m[0].length)
        const e = text.slice(start, end).replace(/-+$/, '').length + start
        if (isWordChar(text[e]) || text[e] === '/') continue
        hits.push({ start, end: e, canonical: text.slice(start, e) })
      }
      for (const m of matches(EXPLICIT, text)) {
        const start = m.index
        if (isWordChar(text[start - 1])) continue
        const end = trimTrailing(text, start, start + m[0].length)
        const name = text.slice(start + 'workstream:'.length, end)
        if (name) hits.push({ start, end, canonical: name })
      }
      return hits
    },
    chipLabel: (ref) => ref.canonical,
    // Workstreams list at /planner/workstreams; /planner/workstreams/:id opens by WS- item id, not by tag name.
    open: () => ({ type: 'route', to: '/planner/workstreams' }),
  }
}
