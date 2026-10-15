import { describe, expect, it } from 'vitest'
import { applyFleetEvent, cwdName, cwdTail, formatDuration, stateDurations, stateSegments, trackTransitions, timelineWindow, workingShare, fleetCounts, parseFleetAgents, sortFleetAgents } from './fleet-agents'

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

  it('applies stream events', () => {
    let rows = parseFleetAgents({ agents: [{ id: 'a', provider: 'claude', pid: 1, cwd: '/x/y', state: 'idle', confidence: { state: 'exact' } }] })
    const ev = (state: string, waiting = '') => ({ session_id: 'a', time: '2026-10-15T07:00:00Z', process: { pid: 1, provider: 'claude' }, payload: { state, waiting_for: waiting, cwd: '/x/y' } })
    rows = applyFleetEvent(rows, 'session.heartbeat', ev('waiting', 'permission prompt'))
    expect(rows[0].state).toBe('waiting')
    expect(rows[0].waitingFor).toBe('permission prompt')
    expect(rows[0].stateConfidence).toBe('exact')
    rows = applyFleetEvent(rows, 'session.processing', ev('busy'))
    expect(rows[0].waitingFor).toBeNull()
    rows = applyFleetEvent(rows, 'process.started', { ...ev('idle'), session_id: 'b' })
    expect(rows.map((r) => r.id)).toEqual(['a', 'b'])
    rows = applyFleetEvent(rows, 'process.exited', { session_id: 'a' })
    expect(rows.map((r) => r.id)).toEqual(['b'])
    expect(applyFleetEvent(rows, 'gap', {})).toBe(rows)
    expect(applyFleetEvent(rows, 'session.idle', 'junk')).toBe(rows)
  })

  it('records transitions the page observes', () => {
    const mk = (state: string, waiting = '') => parseFleetAgents({ agents: [{ id: 'a', state, waiting_for: waiting }] })
    let h = trackTransitions({}, null, mk('idle'), 100)
    expect(h.a.map((t) => [t.kind, t.state])).toEqual([['seen', 'idle']])
    h = trackTransitions(h, mk('idle'), mk('idle'), 150)
    expect(h.a).toHaveLength(1)
    h = trackTransitions(h, mk('idle'), mk('waiting', 'permission prompt'), 200)
    expect(h.a[1]).toMatchObject({ kind: 'state', state: 'waiting', waitingFor: 'permission prompt' })
    const both = parseFleetAgents({ agents: [{ id: 'a', state: 'busy' }, { id: 'b', state: 'idle' }] })
    h = trackTransitions(h, mk('busy'), both, 300)
    expect(h.b[0].kind).toBe('started')
    expect(trackTransitions(h, both, mk('busy'), 400).b).toBeUndefined()
  })

  it('builds the observed timeline and durations', () => {
    const e = (at: number, state: 'idle' | 'busy') => ({ at, kind: 'state' as const, state, waitingFor: null })
    const segs = stateSegments([e(1_000, 'idle'), e(4_000, 'busy')], 10_000, 20_000)
    expect(segs).toEqual([
      { state: null, from: -10_000, to: 1_000 },
      { state: 'idle', from: 1_000, to: 4_000 },
      { state: 'busy', from: 4_000, to: 10_000 },
    ])
    expect(stateDurations(segs)).toMatchObject({ idle: 3_000, busy: 6_000 })
    expect(workingShare([e(1_000, 'idle'), e(4_000, 'busy')], 10_000, 20_000)).toBe(67)
    expect(workingShare([], 10_000, 5_000)).toBeNull()
    expect(stateSegments([], 10_000, 5_000)).toEqual([{ state: null, from: 5_000, to: 10_000 }])
  })

  it('sizes the timeline to what was watched', () => {
    const e = (at: number) => ({ at, kind: 'seen' as const, state: 'idle' as const, waitingFor: null })
    expect(timelineWindow([], 5_000)).toBe(60_000)
    expect(timelineWindow([e(0)], 5 * 60_000)).toBe(5 * 60_000)
    expect(timelineWindow([e(0)], 3 * 3_600_000)).toBe(30 * 60_000)
  })

  it('formats names and durations', () => {
    expect(cwdName('/a/b/nanite-worker-1')).toBe('nanite-worker-1')
    expect(formatDuration(45_000)).toBe('45s')
    expect(formatDuration(4 * 60_000 + 5_000)).toBe('4m 05s')
    expect(formatDuration(3 * 3_600_000 + 12 * 60_000)).toBe('3h 12m')
  })
})
