import { describe, expect, it } from 'vitest'
import { cwdTail, fleetCounts, parseFleetAgents, sortFleetAgents } from './fleet-agents'

describe('fleet agents', () => {
  it('parses rows defensively', () => {
    const rows = parseFleetAgents({
      agents: [
        { id: 'a', provider: 'claude', pid: 1, cwd: '/x/y', state: 'busy', last_activity: '2026-10-15T06:00:00Z', confidence: { state: 'exact' } },
        { id: 'b', state: 'weird' },
        { state: 'idle' },
        null,
      ],
    })
    expect(rows.map((r) => r.id)).toEqual(['a', 'b'])
    expect(rows[0].stateConfidence).toBe('exact')
    expect(rows[1].state).toBe('unknown')
    expect(parseFleetAgents('nope')).toEqual([])
  })

  it('sorts waiting, busy, idle, unknown then by recency', () => {
    const mk = (id: string, state: string, t: string) => ({ id, state, last_activity: t })
    const rows = sortFleetAgents(
      parseFleetAgents({ agents: [mk('i', 'idle', '2026-10-15T01:00:00Z'), mk('u', 'unknown', '2026-10-15T09:00:00Z'), mk('b2', 'busy', '2026-10-15T02:00:00Z'), mk('b1', 'busy', '2026-10-15T03:00:00Z'), mk('w', 'waiting', '2026-10-15T00:00:00Z')] }),
    )
    expect(rows.map((r) => r.id)).toEqual(['w', 'b1', 'b2', 'i', 'u'])
    expect(fleetCounts(rows).busy).toBe(2)
  })

  it('shortens cwd', () => {
    expect(cwdTail('/home/c/dev/apps/torque')).toBe('apps/torque')
  })
})
