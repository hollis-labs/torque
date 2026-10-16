import type { RefKind } from '../types'
import { isWordChar, matches } from '../scan-utils'

// Torque ids look like `CW-20261016-0101`: PREFIX-YYYYMMDD-NNNN.
const ID = /(CW|EP|PRJ|SP|PL)-\d{8}-\d{4}/g

interface TorqueEntity {
  id: string
  prefix: string
  label: string
  icon: string
  route: (id: string) => string
}

export const TORQUE_ENTITIES: TorqueEntity[] = [
  { id: 'torque.task', prefix: 'CW', label: 'Torque task', icon: 'task', route: (id) => `/tasks/${encodeURIComponent(id)}` },
  { id: 'torque.epic', prefix: 'EP', label: 'Torque epic', icon: 'epic', route: (id) => `/epics/${encodeURIComponent(id)}` },
  { id: 'torque.project', prefix: 'PRJ', label: 'Torque project', icon: 'project', route: (id) => `/projects/${encodeURIComponent(id)}` },
  { id: 'torque.sprint', prefix: 'SP', label: 'Torque sprint', icon: 'sprint', route: (id) => `/sprints/${encodeURIComponent(id)}` },
  { id: 'torque.plan', prefix: 'PL', label: 'Torque plan', icon: 'plan', route: (id) => `/plans/${encodeURIComponent(id)}` },
]

/** Is the id standing on its own, not inside a longer token, path or file name? */
function standalone(text: string, start: number, end: number): boolean {
  const before = text[start - 1]
  if (isWordChar(before) || before === '-' || before === '/' || before === '\\' || before === '.' || before === '#') return false
  const after = text[end]
  if (isWordChar(after) || after === '/' || after === '\\') return false
  // `CW-20261016-0101-foo`, `CW-20261016-0101.md`: a longer token or a file name.
  if ((after === '-' || after === '.') && isWordChar(text[end + 1])) return false
  return true
}

/** One kind per Torque entity so each routes, resolves and caches on its own. */
export function createTorqueKinds(): RefKind[] {
  return TORQUE_ENTITIES.map((e) => ({
    id: e.id,
    label: e.label,
    icon: e.icon,
    priority: 80,
    scan(text) {
      const hits = []
      for (const m of matches(ID, text)) {
        if (m[1] !== e.prefix) continue
        const start = m.index
        const end = start + m[0].length
        if (!standalone(text, start, end)) continue
        hits.push({ start, end, canonical: m[0] })
      }
      return hits
    },
    open: (ref) => ({ type: 'route', to: e.route(ref.canonical) }),
  }))
}
