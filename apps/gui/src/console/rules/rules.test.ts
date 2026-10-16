// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import type { ConsoleEvent } from '../events/types'
import type { AlertEntry } from '../alerts/use-alerts'
import { ToastQueue } from '../alerts/toast-queue'
import { DEFAULT_ALERT_PREFS, type Alert } from '../alerts/types'
import { applyEscalation, coalesceEntries } from './coalesce'
import { DEFAULT_RULE_PACK } from './default-pack'
import { createRuleMapper, renderAlertTemplate } from './engine'
import { parseRulePack } from './parse'

const ev = (over: Partial<ConsoleEvent>): ConsoleEvent => ({
  id: 'e1', ts: 1000, kind: 'x.y', source: { id: 'fixture', machine: 'hub' }, subjects: [], severity: 'info', payload: {}, actions: [], extra: {}, ...over,
})
const map = createRuleMapper(DEFAULT_RULE_PACK)

describe('default rule pack', () => {
  it('round-trips through JSON and the validator with no problems', () => {
    const r = parseRulePack(JSON.parse(JSON.stringify(DEFAULT_RULE_PACK)))
    expect(r.problems).toEqual([])
    expect(r.pack.rules).toHaveLength(DEFAULT_RULE_PACK.rules.length)
  })
  it('agent waiting is a warning that escalates; not-waiting and metrics drop', () => {
    const a = map(ev({ kind: 'agent.state', payload: { state: 'waiting', waitingFor: 'permission' }, subjects: [{ type: 'agent', id: 'a1', label: 'Alpha' }] }))
    expect(a).toMatchObject({ severity: 'warning', title: 'Alpha is waiting', detail: 'permission', coalesceKey: 'waiting:Alpha', ruleId: 'agent-waiting' })
    expect(a?.escalate).toEqual({ afterMs: 600_000, to: 'error' })
    expect(map(ev({ kind: 'agent.state', payload: { state: 'busy' } }))).toBeNull()
    expect(map(ev({ kind: 'metric.tick', severity: 'error' }))).toBeNull()
  })
  it('task failed vs review vs other, PR, budget, checkpoint, message', () => {
    const t = (to: string) => map(ev({ kind: 'task.state_changed', payload: { to }, subjects: [{ type: 'task', id: 'CW-1' }] }))
    expect(t('failed')?.severity).toBe('error')
    expect(t('review')?.severity).toBe('notice')
    expect(t('done')?.severity).toBe('info')
    expect(map(ev({ kind: 'pr.review_requested', payload: { repo: 'o/r', number: 4 } }))?.title).toBe('o/r#4 needs review')
    expect(map(ev({ kind: 'pr.checks_failed', payload: { repo: 'o/r', number: 4 } }))?.severity).toBe('error')
    expect(map(ev({ kind: 'budget.threshold', payload: { percent: 85, scope: 'agents' } }))?.severity).toBe('warning')
    expect(map(ev({ kind: 'budget.threshold', payload: { percent: 120, scope: 'agents' } }))?.severity).toBe('critical')
    expect(map(ev({ kind: 'budget.threshold', payload: { percent: 10 } }))).toBeNull()
    expect(map(ev({ kind: 'checkpoint.state_changed', payload: { to: 'pending' }, subjects: [{ type: 'checkpoint', id: 'c', label: 'CP' }] }))?.title).toBe('Checkpoint pending: CP')
    expect(map(ev({ kind: 'message.received', payload: { from: 'bravo', preview: 'hi' } }))).toMatchObject({ title: 'New message', coalesceKey: 'msg:bravo' })
    expect(map(ev({ kind: 'message.delivered' }))).toBeNull()
  })
  it('unknown kinds degrade by severity', () => {
    expect(map(ev({ severity: 'info' }))).toBeNull()
    expect(map(ev({ severity: 'error' }))).toMatchObject({ severity: 'error', icon: 'error', title: 'x.y' })
  })
  it('matches on v1.1 class fields and honours enabled=false', () => {
    const rules = createRuleMapper([
      { id: 'off', enabled: false, match: { kinds: ['k.*'] }, alert: { title: 'off' } },
      { id: 'lab', match: { labels: ['needs-attention'], categories: ['state'] }, alert: { severity: 'warning', title: 'L' } },
    ])
    expect(rules(ev({ kind: 'k.a', class: { category: 'state', labels: ['needs-attention'] } }))?.title).toBe('L')
    expect(rules(ev({ kind: 'k.a', class: { category: 'data', labels: ['needs-attention'] } }))).toBeNull()
  })
  it('templates are closed: no code, fallbacks, whitespace collapsed', () => {
    const e = ev({ payload: { a: 'x\n y', n: 3, o: { z: 1 } } })
    expect(renderAlertTemplate('{{payload.a}} {{payload.n}} [{{payload.o}}] {{payload.q|none}} {{payload.q,payload.n}}', e)).toBe('x y 3 [] none 3')
    expect(renderAlertTemplate('{{constructor.name}}{{payload.__proto__}}', e)).toBe('')
  })
})

describe('parseRulePack', () => {
  it('skips bad rules with reasons and rejects other versions', () => {
    const r = parseRulePack({ version: 1, id: 'p', rules: [{ id: 'ok', match: {}, alert: { title: 't' } }, { id: 'ok', match: {}, alert: { title: 't' } }, { id: 'b', match: {}, alert: { title: 't', severity: 'loud' } }, { match: {} }] })
    expect(r.pack.rules.map((x) => x.id)).toEqual(['ok'])
    expect(r.problems).toHaveLength(3)
    expect(parseRulePack({ version: 2, rules: [] }).pack.rules).toEqual([])
    expect(parseRulePack(null).problems.length).toBeGreaterThan(0)
  })
})

const entry = (id: string, ts: number, key?: string, over: Partial<Alert> = {}, status: AlertEntry['status'] = 'unread'): AlertEntry => ({
  alert: { id, severity: 'notice', icon: 'info', title: 'T', source: 's', ts, kind: 'k', coalesceKey: key, ...over }, status,
})

describe('coalescing and escalation', () => {
  it('folds a burst into the newest entry with a count, chained by gap', () => {
    const out = coalesceEntries([entry('c', 300_000, 'k'), entry('b', 250_000, 'k'), entry('a', 200_000, 'k'), entry('z', 1_000, 'k'), entry('n', 5, undefined)], 120_000)
    expect(out.map((e) => e.alert.id)).toEqual(['c', 'z', 'n'])
    expect(out[0].alert).toMatchObject({ count: 3, title: 'T (×3)' })
  })
  it('does not fold across statuses, and 0 disables', () => {
    const list = [entry('b', 20, 'k'), entry('a', 10, 'k', {}, 'dismissed')]
    expect(coalesceEntries(list, 1000)).toHaveLength(2)
    expect(coalesceEntries([entry('b', 20, 'k'), entry('a', 10, 'k')], 0)).toHaveLength(2)
  })
  it('escalates only unread alerts past their delay', () => {
    const e = entry('a', 0, undefined, { escalate: { afterMs: 1000, to: 'error' } })
    expect(applyEscalation([e], 500)[0].alert.severity).toBe('notice')
    expect(applyEscalation([e], 1500)[0].alert.severity).toBe('error')
    expect(applyEscalation([{ ...e, status: 'read' }], 1500)[0].alert.severity).toBe('notice')
  })
})

describe('toast routing: quiet, quiet hours, mute', () => {
  const al = (id: string, severity: Alert['severity'], over: Partial<Alert> = {}): Alert => ({ id, severity, icon: 'info', title: id, source: 'agent-bravo', ts: 1, kind: 'k', ...over })
  const run = (prefs: Partial<typeof DEFAULT_ALERT_PREFS>, alerts: Alert[], now = new Date(2026, 9, 16, 12, 0).getTime()) => {
    const q = new ToastQueue()
    q.offer([], DEFAULT_ALERT_PREFS)
    q.offer(alerts, { ...DEFAULT_ALERT_PREFS, ...prefs }, now)
    return q.getSnapshot().map((a) => a.id)
  }
  it('critical breaks through quiet mode; others do not', () => {
    expect(run({ quiet: true }, [al('e', 'error'), al('c', 'critical')])).toEqual(['c'])
  })
  it('quiet hours behave like quiet mode inside the window (wrapping midnight) only', () => {
    const q = { quietHoursEnabled: true, quietHoursStart: '22:00', quietHoursEnd: '07:00' }
    const at = (h: number) => new Date(2026, 9, 16, h, 0).getTime()
    expect(run(q, [al('e', 'error'), al('c', 'critical')], at(23))).toEqual(['c'])
    expect(run(q, [al('e', 'error')], at(3))).toEqual([])
    expect(run(q, [al('e', 'error')], at(12))).toEqual(['e'])
  })
  it('muted sources hold back toasts except critical', () => {
    expect(run({ mutedSources: ['bravo'] }, [al('w', 'warning'), al('c', 'critical')])).toEqual(['c'])
    expect(run({ mutedSources: ['bravo'] }, [al('w', 'warning', { source: 'alpha' })])).toEqual(['w'])
  })
  it('a burst with one coalesce key updates a single toast', () => {
    const q = new ToastQueue()
    q.offer([], DEFAULT_ALERT_PREFS)
    q.offer([al('a', 'warning', { coalesceKey: 'k' })], DEFAULT_ALERT_PREFS)
    q.offer([al('b', 'warning', { coalesceKey: 'k', count: 2 })], DEFAULT_ALERT_PREFS)
    expect(q.getSnapshot().map((a) => a.id)).toEqual(['b'])
  })
})
