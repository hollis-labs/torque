// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { ApiProvider } from '@/hooks/use-api'
import { CardRuntimeProvider, CardTray } from '../cards/surfaces'
import { createDefaultCardRegistry } from '../cards/kinds'
import { FixtureSource } from '../events/fixture-source'
import { ConsoleProvider } from '../events/provider'
import type { ConsoleEvent } from '../events/types'
import { createActionBus, type ActionRecord } from '../seams/action-bus'
import { MockIdentityConfirm } from '../seams/identity-confirm'
import { MockWorkflowClient, WorkflowError } from './client'
import { workflowFixtures } from './fixtures'
import { flowAct, flowCard, flowOnEvent, flowStart, flowTimeout, SAMPLE_BLOCKED_FLOW, validateFlow } from './flow'
import { registerWorkflowKinds } from './kinds'
import { applyRunEvent, runToCards } from './model'
import { WorkflowProvider } from './provider'

afterEach(cleanup)
const T0 = 1_000_000_000_000
const fixtures = () => workflowFixtures(T0)
const ev = (id: string, kind: string, payload: Record<string, unknown>, ts = T0): ConsoleEvent => ({
  id, ts, kind, source: { id: 'test' }, subjects: [{ type: 'run', id: String(payload.runId) }], severity: 'info', payload, actions: [], extra: {},
})
const granted = () => new MockIdentityConfirm({ now: () => T0 }).confirm({ what: { action: 'a', title: 't' }, scope: ['workflow:steer'] })

describe('MockWorkflowClient', () => {
  it('pause/resume/escalate need no step-up; redirect and priority do, and the audit records the step-up without the token', async () => {
    const c = new MockWorkflowClient(fixtures(), { now: () => T0 })
    expect((await c.steer('run-1', { type: 'pause' }, {})).state).toBe('paused')
    expect((await c.steer('run-1', { type: 'resume' }, {})).state).toBe('running')
    expect((await c.steer('run-1', { type: 'escalate', to: 'owner' }, {})).escalatedTo).toBe('owner')
    await expect(c.steer('run-1', { type: 'redirect', instruction: 'go' }, {})).rejects.toMatchObject({ code: 'confirm_required' })
    await expect(c.steer('run-1', { type: 'priority', priority: 'urgent' }, {})).rejects.toBeInstanceOf(WorkflowError)
    const confirmation = await granted()
    const r = await c.steer('run-1', { type: 'redirect', instruction: ' skip tests ' }, { confirmation })
    expect(r.redirect).toBe('skip tests')
    expect(r.audit.map((a) => a.action).slice(-2)).toEqual(['confirmed', 'steer:redirect'])
    expect(JSON.stringify(r)).not.toContain(confirmation.token!)
    await expect(c.steer('run-4', { type: 'pause' }, {})).rejects.toMatchObject({ code: 'not_active' })
  })

  it('gates: fail is step-up free, pass needs it, override needs a reason, and a pass activates the next phase', async () => {
    const confirmation = await granted()
    const c = new MockWorkflowClient(fixtures(), { now: () => T0 })
    await expect(c.submitGate('run-1', 'g3', 'pass', undefined, {})).rejects.toMatchObject({ code: 'confirm_required' })
    await expect(c.submitGate('run-1', 'g3', 'override', ' ', { confirmation })).rejects.toMatchObject({ code: 'invalid' })
    const passed = await c.submitGate('run-1', 'g3', 'override', 'rollback doc follows', { confirmation })
    expect(passed.phases[2].gate?.status).toBe('overridden')
    expect(passed.phases[3].status).toBe('active')
    await expect(c.submitGate('run-1', 'g3', 'pass', undefined, { confirmation })).rejects.toMatchObject({ code: 'conflict' })
    const c2 = new MockWorkflowClient(fixtures(), { now: () => T0 })
    const failed = await c2.submitGate('run-1', 'g3', 'fail', 'not ready', {})
    expect(failed).toMatchObject({ state: 'blocked', blockedReason: expect.stringContaining('failed') })
  })

  it('plans: approve needs step-up and at least one phase; unticked phases are recorded as an edit; reject needs none', async () => {
    const confirmation = await granted()
    const c = new MockWorkflowClient(fixtures(), { now: () => T0 })
    await expect(c.decidePlan('run-2', 'approve', ['i1'], undefined, {})).rejects.toMatchObject({ code: 'confirm_required' })
    await expect(c.decidePlan('run-2', 'approve', [], undefined, { confirmation })).rejects.toMatchObject({ code: 'invalid' })
    const r = await c.decidePlan('run-2', 'approve', ['i1', 'i2'], 'keep legacy', { confirmation })
    expect(r.plan?.items.map((i) => i.included)).toEqual([true, true, false])
    expect(r).toMatchObject({ state: 'running' })
    expect(r.audit.map((a) => a.action)).toContain('plan:approved-edited')
    const rej = await new MockWorkflowClient(fixtures(), { now: () => T0 }).decidePlan('run-2', 'reject', [], 'too broad', {})
    expect(rej.plan?.status).toBe('rejected')
  })

  it('checkpoints validate the answer against the options unless free text is allowed', async () => {
    const c = new MockWorkflowClient(fixtures(), { now: () => T0 })
    await expect(c.respondCheckpoint('run-3', 'cp-1', ' ', {})).rejects.toMatchObject({ code: 'invalid' })
    const r = await c.respondCheckpoint('run-3', 'cp-1', 'defer', {})
    expect(r).toMatchObject({ state: 'running', checkpoint: { status: 'answered', answer: 'defer' } })
    await expect(c.respondCheckpoint('run-3', 'cp-1', 'yes', {})).rejects.toMatchObject({ code: 'conflict' })
  })
})

describe('event folding', () => {
  it('builds a run from phase/gate/progress/state events and ignores unknown kinds', () => {
    let runs = new Map<string, ReturnType<typeof fixtures>[number]>()
    const apply = (e: ConsoleEvent) => {
      const r = applyRunEvent(runs, e)
      if (r) runs = new Map(runs).set(r.id, r)
    }
    apply(ev('1', 'phase.started', { runId: 'r9', phase: { id: 'a', label: 'Build' } }, 1))
    apply(ev('2', 'progress.checkpoint', { runId: 'r9', phaseId: 'a', done: 2, total: 4 }, 2))
    apply(ev('3', 'gate.evaluated', { runId: 'r9', phaseId: 'a', gateId: 'g', label: 'Build gate', criteria: [{ label: 'lint', met: false }] }, 3))
    apply(ev('4', 'run.state_changed', { runId: 'r9', to: 'blocked', reason: 'lint' }, 4))
    apply(ev('5', 'something.else', { runId: 'r9' }, 5))
    const run = runs.get('r9')!
    expect(run.phases[0]).toMatchObject({ status: 'active', progress: { done: 2, total: 4 }, gate: { id: 'g', status: 'open', requiresHuman: true } })
    expect(run).toMatchObject({ state: 'blocked', blockedReason: 'lint', updatedAt: 4 })
    expect(applyRunEvent(runs, { ...ev('6', 'phase.started', {}, 6), subjects: [] })).toBeUndefined()
  })

  it('derives the card set from a run: human gates, plan and checkpoint only when present, steer only while active', () => {
    const [r1, r2, r3, r4] = fixtures()
    expect(runToCards(r1).map((c) => c.kind)).toEqual(['workflow.progress', 'workflow.steer', 'workflow.gate', 'workflow.gate'])
    expect(runToCards(r1).filter((c) => c.kind === 'workflow.gate').map((c) => c.phase)).toEqual(['resolved', 'live'])
    expect(runToCards(r2).map((c) => c.kind)).toContain('workflow.plan')
    expect(runToCards(r3).map((c) => c.kind)).toContain('workflow.checkpoint')
    expect(runToCards(r4).map((c) => c.kind)).toEqual(['workflow.progress'])
    expect(runToCards(r4)[0].phase).toBe('resolved')
  })
})

describe('flow definitions', () => {
  it('validates the sample and rejects broken definitions with precise errors', () => {
    expect(validateFlow(SAMPLE_BLOCKED_FLOW)).toMatchObject({ ok: true, errors: [] })
    const bad = validateFlow({ id: 'x', version: 1, title: 't', start: 'a', steps: [{ id: 'a', trigger: { immediate: true }, card: { title: 'x' }, actions: [{ id: 'go', label: 'Go', next: 'nope' }] }] })
    expect(bad.ok).toBe(false)
    expect(bad.errors).toEqual(expect.arrayContaining([expect.stringContaining('unknown next "nope"'), expect.stringContaining('start step needs an event trigger')]))
    expect(validateFlow('nope').ok).toBe(false)
    expect(validateFlow({ ...SAMPLE_BLOCKED_FLOW, steps: [...SAMPLE_BLOCKED_FLOW.steps, { id: 'orphan', trigger: { immediate: true }, card: { title: 'o' }, actions: [] }] }).warnings).toContain('step "orphan" is unreachable')
  })

  it('walks trigger -> card -> action -> next, templating runId/payload/input, and times out', () => {
    const def = SAMPLE_BLOCKED_FLOW
    const blocked = ev('b', 'run.state_changed', { runId: 'run-3', to: 'blocked', reason: 'stuck' })
    expect(flowStart(def, ev('x', 'run.state_changed', { runId: 'run-3', to: 'running' }), 'i1')).toBeUndefined()
    let inst = flowStart(def, blocked, 'i1')!
    expect(inst).toMatchObject({ stepId: 'alert', status: 'presenting', runId: 'run-3' })
    expect(flowCard(def, inst)?.title).toBe('Agent blocked: stuck')
    expect(() => flowAct(def, inst, 'bogus', undefined, T0)).toThrow()
    inst = flowAct(def, inst, 'triage', undefined, T0).inst
    expect(inst).toMatchObject({ stepId: 'decide', status: 'presenting' })
    inst = flowAct(def, inst, 'redirect', undefined, T0).inst
    expect(() => flowAct(def, inst, 'send', ' ', T0)).toThrow(/required/)
    const out = flowAct(def, inst, 'send', ' try the other branch ', T0)
    expect(out.dispatch).toEqual({ kind: 'workflow.steer', params: { runId: 'run-3', command: { type: 'redirect', instruction: 'try the other branch' } } })
    expect(out.inst).toMatchObject({ stepId: 'resolved', status: 'waiting' })
    expect(flowCard(def, out.inst)).toBeUndefined()
    const done = flowOnEvent(def, out.inst, ev('r', 'run.state_changed', { runId: 'run-3', to: 'running' }))
    expect(done).toMatchObject({ status: 'ended', outcome: 'Agent unblocked and running again' })
    // timeout on the first step escalates
    const fresh = flowStart(def, blocked, 'i2')!
    expect(flowTimeout(def, fresh, T0 + 1000)).toBe(fresh)
    expect(flowTimeout(def, fresh, T0 + 1_800_000)).toMatchObject({ stepId: 'escalate', status: 'presenting' })
  })
})

function setup(opts: { mode?: 'auto-grant' | 'auto-deny' } = {}) {
  const identity = new MockIdentityConfirm({ mode: opts.mode ?? 'auto-grant' })
  const bus = createActionBus({ identity })
  const records: ActionRecord[] = []
  bus.subscribe((r) => records.push(r))
  const registry = createDefaultCardRegistry()
  registerWorkflowKinds(registry)
  const client = new MockWorkflowClient(workflowFixtures(Date.now()))
  const source = new FixtureSource({ intervalMs: 0, initial: 0 })
  const flows = [SAMPLE_BLOCKED_FLOW]
  const ui = (
    <ApiProvider client={{} as never}>
      <ConsoleProvider source={source} identity={identity} actionBus={bus} resyncMs={0}>
        <CardRuntimeProvider registry={registry} mountSurfaces={false}>
          <WorkflowProvider client={client} flows={flows} tickMs={0}>
            <CardTray includeClosed />
          </WorkflowProvider>
        </CardRuntimeProvider>
      </ConsoleProvider>
    </ApiProvider>
  )
  return { ui, client, records, source }
}

const open = async (title: string) => {
  await act(async () => { (await screen.findByText(title)).click() })
}

describe('workflow cards', () => {
  it('steer: pause directly, redirect through step-up, denied step-up never reaches the client', async () => {
    const { ui, client } = setup()
    await act(async () => { render(ui) })
    await open('Steer: Migrate planner schema')
    const card = within(screen.getByLabelText('Steer: Steer: Migrate planner schema'))
    await act(async () => { card.getByText('Pause').click() })
    await waitFor(async () => expect((await client.get('run-1'))?.state).toBe('paused'))
    await act(async () => { (await screen.findByText('Resume')).click() })
    await waitFor(async () => expect((await client.get('run-1'))?.state).toBe('running'))
    await act(async () => { card.getByText('Redirect*').click() })
    await act(async () => { fireEvent.change(card.getByLabelText(/New instruction/), { target: { value: 'use the new schema' } }) })
    await act(async () => { card.getByText('Send redirect').click() })
    await waitFor(async () => expect((await client.get('run-1'))?.redirect).toBe('use the new schema'))
    expect((await client.get('run-1'))!.audit.map((a) => a.action)).toContain('confirmed')
  })

  it('steer: a denied step-up shows the error and changes nothing', async () => {
    const { ui, client } = setup({ mode: 'auto-deny' })
    await act(async () => { render(ui) })
    await open('Steer: Migrate planner schema')
    const card = within(screen.getByLabelText('Steer: Steer: Migrate planner schema'))
    await act(async () => { card.getByText('Redirect*').click() })
    await act(async () => { fireEvent.change(card.getByLabelText(/New instruction/), { target: { value: 'x' } }) })
    await act(async () => { card.getByText('Send redirect').click() })
    expect((await screen.findByRole('alert')).textContent).toMatch(/mock policy/)
    expect((await client.get('run-1'))?.redirect).toBeUndefined()
  })

  it('gate: override stays disabled without a note, then passes with step-up and the card closes', async () => {
    const { ui, client } = setup()
    await act(async () => { render(ui) })
    await open('Release gate: Verify')
    const card = within(screen.getByLabelText('Gate: Release gate: Verify'))
    expect(card.getAllByRole('listitem').filter((li) => li.getAttribute('data-criterion-met') === 'false')).toHaveLength(1)
    expect((card.getByText('Override*') as HTMLButtonElement).disabled).toBe(true)
    await act(async () => { fireEvent.change(card.getByLabelText(/Note/), { target: { value: 'doc follows' } }) })
    await act(async () => { card.getByText('Override*').click() })
    await waitFor(async () => expect((await client.get('run-1'))?.phases[2].gate?.status).toBe('overridden'))
    expect((await screen.findAllByText('Overridden')).length).toBeGreaterThan(0)
  })

  it('plan: dropping a phase edits the approval', async () => {
    const { ui, client } = setup()
    await act(async () => { render(ui) })
    await open('Plan: Refactor messaging client')
    const card = within(screen.getByLabelText('Plan: Plan: Refactor messaging client'))
    await act(async () => { fireEvent.click(card.getByLabelText(/Delete legacy path/)) })
    await act(async () => { card.getByText(/Approve 2 of 3/).click() })
    await waitFor(async () => expect((await client.get('run-2'))?.plan?.status).toBe('approved'))
    expect((await client.get('run-2'))!.plan!.items.map((i) => i.included)).toEqual([true, true, false])
  })

  it('checkpoint: pick an option and send', async () => {
    const { ui, client } = setup()
    await act(async () => { render(ui) })
    await open('Upgrade react-router to v8?')
    const send = screen.getByText('Send answer') as HTMLButtonElement
    expect(send.disabled).toBe(true)
    await act(async () => { fireEvent.click(screen.getByLabelText(/Defer to next sprint/)) })
    await act(async () => { send.click() })
    await waitFor(async () => expect((await client.get('run-3'))?.checkpoint?.answer).toBe('defer'))
  })

  it('progress: shows phases with gate chips and a progress bar for the active phase', async () => {
    const { ui } = setup()
    await act(async () => { render(ui) })
    await open('Migrate planner schema')
    const card = within(screen.getByLabelText('Workflow: Migrate planner schema'))
    expect(card.getByText(/1\/4/)).toBeTruthy()
    expect(card.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('38')
    expect(card.getAllByText(/^gate /).map((e) => e.getAttribute('data-gate-status'))).toEqual(['passed', 'open'])
  })
})

describe('end to end: agent blocks -> alert -> decision -> steer -> resolution', () => {
  it('runs the sample flow against the event feed', async () => {
    const { ui, client, source, records } = setup()
    await act(async () => { render(ui) })
    await act(async () => { source.emit(ev('blk', 'run.state_changed', { runId: 'run-3', to: 'blocked', reason: 'lint failing' }, Date.now())) })
    await open('Agent blocked: lint failing')
    await act(async () => { screen.getByText('Triage').click() })
    await act(async () => { (await screen.findByText('Give new instructions')).click() })
    const send = (await screen.findByText('Redirect')) as HTMLButtonElement
    expect(send.disabled).toBe(true)
    await act(async () => { fireEvent.change(screen.getByLabelText(/Instruction/), { target: { value: 'skip lint for now' } }) })
    await act(async () => { send.click() })
    await waitFor(async () => expect((await client.get('run-3'))?.redirect).toBe('skip lint for now'))
    // a single step-up covered the inner dispatch
    expect(records.filter((r) => r.action.kind === 'workflow.steer' && r.result.confirmation?.outcome === 'granted')).toHaveLength(1)
    await act(async () => { source.emit(ev('run', 'run.state_changed', { runId: 'run-3', to: 'running' }, Date.now() + 1)) })
    expect((await screen.findAllByText('Agent unblocked and running again')).length).toBeGreaterThan(0)
  })
})
