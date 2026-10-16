// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { KINDS, type ConsoleEvent } from '../events/types'
import { isConsoleEnabled } from './enabled'
import { defaultAlertMapper } from './mapping'
import { safeHref, severityAtLeast } from './policy'
import { MemoryAlertPrefsStore, LocalAlertPrefsStore, ALERT_PREFS_STORAGE_KEY } from './prefs'
import { ToastQueue } from './toast-queue'
import { DEFAULT_ALERT_PREFS, type Alert } from './types'

const ev = (over: Partial<ConsoleEvent>): ConsoleEvent => ({
  id: 'e1', ts: 1000, kind: 'x.y', source: { id: 'fixture', machine: 'hub' }, subjects: [], severity: 'info', payload: {}, actions: [], extra: {}, ...over,
})
const al = (id: string, severity: Alert['severity'] = 'notice', ts = 1): Alert => ({ id, severity, icon: 'info', title: id, source: 's', ts, kind: 'k' })

describe('defaultAlertMapper', () => {
  it('maps known kinds and drops noise', () => {
    expect(defaultAlertMapper(ev({ kind: KINDS.metricTick }))).toBeNull()
    expect(defaultAlertMapper(ev({ kind: KINDS.agentState, payload: { state: 'busy' } }))).toBeNull()
    const w = defaultAlertMapper(ev({ kind: KINDS.agentState, payload: { state: 'waiting', waitingFor: 'approval' }, subjects: [{ type: 'agent', id: 'a1' }] }))
    expect(w?.title).toBe('a1 is waiting')
    expect(w?.primary?.href).toBe('/planner/agents')
    const t = defaultAlertMapper(ev({ kind: KINDS.taskStateChanged, payload: { to: 'review', from: 'in_progress' }, subjects: [{ type: 'task', id: 'CW-1' }] }))
    expect(t?.title).toBe('CW-1 → review')
    expect(t?.primary?.href).toBe('/tasks/CW-1')
  })
  it('degrades unknown kinds by severity and tolerates junk payloads', () => {
    expect(defaultAlertMapper(ev({ severity: 'info' }))).toBeNull()
    expect(defaultAlertMapper(ev({ severity: 'error' }))?.icon).toBe('error')
    expect(defaultAlertMapper(ev({ kind: KINDS.messageReceived, payload: { subject: 42, preview: { a: 1 } } }))?.title).toBe('Message from agent')
  })
  it('clamps detail to one line and rejects unsafe links', () => {
    const a = defaultAlertMapper(ev({ kind: KINDS.messageReceived, payload: { subject: 's', preview: 'a\n\nb ' + 'x'.repeat(300) } }))
    expect(a?.detail).not.toContain('\n')
    expect(a!.detail!.length).toBeLessThanOrEqual(140)
    const l = defaultAlertMapper(ev({ kind: KINDS.messageReceived, subjects: [{ type: 'pr', id: '1', url: 'javascript:alert(1)' }] }))
    expect(l?.primary).toBeUndefined()
    expect(safeHref('//evil.com')).toBeNull()
    expect(safeHref('/tasks/1')).toBe('/tasks/1')
    expect(safeHref('https://github.com/x')).toBe('https://github.com/x')
  })
})

describe('ToastQueue', () => {
  it('does not toast the backlog, toasts new alerts above the threshold, and respects quiet', () => {
    const q = new ToastQueue()
    q.offer([al('old')], DEFAULT_ALERT_PREFS)
    expect(q.getSnapshot()).toHaveLength(0)
    q.offer([al('old'), al('low', 'info'), al('new', 'warning')], DEFAULT_ALERT_PREFS)
    expect(q.getSnapshot().map((a) => a.id)).toEqual(['new'])
    q.offer([al('old'), al('new'), al('q', 'error')], { ...DEFAULT_ALERT_PREFS, quiet: true })
    expect(q.getSnapshot()).toHaveLength(0)
  })
  it('does not re-toast a closed alert and drops toasts for handled alerts', () => {
    const q = new ToastQueue()
    q.offer([], DEFAULT_ALERT_PREFS)
    q.offer([al('a'), al('b')], DEFAULT_ALERT_PREFS)
    q.close('a')
    q.offer([al('a'), al('b')], DEFAULT_ALERT_PREFS)
    expect(q.getSnapshot().map((x) => x.id)).toEqual(['b'])
    q.offer([al('a')], DEFAULT_ALERT_PREFS)
    expect(q.getSnapshot()).toHaveLength(0)
  })
  it('caps the stack but keeps criticals', () => {
    const q = new ToastQueue()
    q.offer([], DEFAULT_ALERT_PREFS)
    q.offer([al('c', 'critical', 1), ...[2, 3, 4, 5, 6].map((n) => al(`n${n}`, 'notice', n))], DEFAULT_ALERT_PREFS)
    const ids = q.getSnapshot().map((a) => a.id)
    expect(ids).toHaveLength(4)
    expect(ids).toContain('c')
  })
})

describe('prefs and gate', () => {
  it('sanitizes and persists prefs', () => {
    window.localStorage.setItem(ALERT_PREFS_STORAGE_KEY, JSON.stringify({ quiet: 'yes', toastMinSeverity: 'bogus' }))
    const s = new LocalAlertPrefsStore()
    expect(s.get()).toEqual(DEFAULT_ALERT_PREFS)
    s.set({ quiet: true })
    expect(JSON.parse(window.localStorage.getItem(ALERT_PREFS_STORAGE_KEY)!).quiet).toBe(true)
    expect(new MemoryAlertPrefsStore({ toastMinSeverity: 'error' }).get().toastMinSeverity).toBe('error')
  })
  it('is enabled only by an explicit console mode', () => {
    expect(isConsoleEnabled({ search: '' })).toBe(false)
    expect(isConsoleEnabled({ search: '?console=nope', env: 'x' })).toBe(false)
    expect(isConsoleEnabled({ search: '?console=fixture' })).toBe(true)
    expect(isConsoleEnabled({ env: 'live' })).toBe(true)
    expect(severityAtLeast('warning', 'notice')).toBe(true)
  })
})
