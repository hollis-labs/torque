import { describe, expect, it } from 'vitest'
import { parseConsoleEvent } from './parse'

describe('parseConsoleEvent', () => {
  it('rejects non-events', () => {
    expect(parseConsoleEvent(null)).toBeNull()
    expect(parseConsoleEvent('x')).toBeNull()
    expect(parseConsoleEvent({ kind: 'a' })).toBeNull()
    expect(parseConsoleEvent({ id: '1' })).toBeNull()
  })

  it('fills defaults for a minimal event', () => {
    const e = parseConsoleEvent({ id: '1', kind: 'x.y' }, { now: () => 42 })!
    expect(e).toMatchObject({ id: '1', kind: 'x.y', ts: 42, severity: 'info', subjects: [], actions: [], payload: {}, extra: {} })
    expect(e.source.id).toBe('unknown')
  })

  it('accepts envelope aliases and preserves unknown fields additively', () => {
    const e = parseConsoleEvent({
      id: 'a', kind: 'k', time: '2026-10-16T00:00:00Z', severity: 'WARN', thread_id: 't', correlation_id: 'c',
      source: 'torque', newThing: { v: 1 },
    })!
    expect(e.ts).toBe(Date.parse('2026-10-16T00:00:00Z'))
    expect(e.severity).toBe('warning')
    expect(e.threadId).toBe('t')
    expect(e.correlationId).toBe('c')
    expect(e.source.id).toBe('torque')
    expect(e.extra).toEqual({ newThing: { v: 1 } })
  })

  it('drops malformed refs and actions but keeps the event', () => {
    const e = parseConsoleEvent({
      id: 'a', kind: 'k', severity: 'bogus',
      subjects: [{ type: 'task', id: 'T1' }, { type: 'x' }, 7],
      actions: [{ id: 'ok', kind: 'approve', requires_confirm: true, scope: ['a'] }, { id: 'no-kind' }],
    })!
    expect(e.severity).toBe('info')
    expect(e.subjects).toEqual([{ type: 'task', id: 'T1' }])
    expect(e.actions).toEqual([{ id: 'ok', label: 'ok', kind: 'approve', requiresConfirm: true, scope: ['a'] }])
  })
})
