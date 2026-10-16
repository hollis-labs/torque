import type { RefKind } from '../types'
import { isWordChar, matches, trimTrailing } from '../scan-utils'

// `@claude-code` mentions and explicit `agent:name`. Emails (`a@b.io`) and
// scoped packages (`@hollis-labs/ui`) are not agents.
const MENTION = /@[A-Za-z][A-Za-z0-9_.-]{1,38}/g
const EXPLICIT = /agent:([A-Za-z][A-Za-z0-9_.-]{1,38})/g

export function createAgentKind(): RefKind {
  return {
    id: 'agent',
    label: 'Agent',
    icon: 'agent',
    priority: 50,
    scan(text) {
      const hits = []
      for (const m of matches(MENTION, text)) {
        const start = m.index
        const before = text[start - 1]
        if (isWordChar(before) || before === '@' || before === '/' || before === '.' || before === '-' || before === ':') continue
        const end = trimTrailing(text, start, start + m[0].length)
        const e = text.slice(start, end).replace(/[-.]+$/, '').length + start
        if (e - start < 3) continue
        if (text[e] === '/' || text[e] === '@') continue
        hits.push({ start, end: e, canonical: text.slice(start + 1, e) })
      }
      for (const m of matches(EXPLICIT, text)) {
        const start = m.index
        if (isWordChar(text[start - 1])) continue
        const end = trimTrailing(text, start, start + m[0].length)
        const e = text.slice(start, end).replace(/[-.]+$/, '').length + start
        hits.push({ start, end: e, canonical: text.slice(start + 'agent:'.length, e) })
      }
      return hits
    },
    chipLabel: (ref) => `@${ref.canonical}`,
    // The Agents tab selects an agent by id.
    open: (ref) => ({ type: 'route', to: `/planner?tab=agents&agent=${encodeURIComponent(ref.canonical)}` }),
  }
}
