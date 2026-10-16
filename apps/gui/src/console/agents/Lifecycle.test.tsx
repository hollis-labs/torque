/** @vitest-environment jsdom */
import { afterEach, expect, it } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AgentDirectoryView } from './AgentDirectory'
import { AgentsClientProvider } from './context'
import { AgentEditProvider, resolveStepUp } from './edit-context'
import { SeamsContext } from '../events/context'
import { computeLifecyclePlan, blankDraft, computeRetireImpact } from './lifecycle'
import { MOCK_ASSIGNMENTS, MOCK_TEAMS } from './lifecycle-fixtures'
import { createMockAgentsClient, MOCK_AGENTS } from './mock'
import { createActionBus, MemoryStateStore, MockIdentityConfirm } from '../seams'
import type { ConfirmResult } from '../seams'

afterEach(cleanup)

const ctx = () => ({ agents: MOCK_AGENTS, teams: MOCK_TEAMS, assignments: MOCK_ASSIGNMENTS })
const meta = { id: 'p', now: 1, actor: { id: 'u' } }
const grant = (action: string, scope: string[]): ConfirmResult => ({ outcome: 'granted', who: { id: 'u' }, what: { action, title: action }, scope, issuedAt: Date.now(), expiresAt: Date.now() + 60_000, token: 't' })

async function runAll(c: ReturnType<typeof createMockAgentsClient>, plan: Awaited<ReturnType<typeof c.planLifecycle>>) {
  await c.approvePlan(plan.id, grant('agent.plan.approve', plan.requiredScope))
  return c.applyPlan(plan.id, grant('agent.plan.apply', plan.requiredScope))
}

it('flags privileged creates as high risk and needs a reason; safe creates are low', () => {
  const safe = computeLifecyclePlan({ op: 'agent.create', agentId: 'scout', name: 'Scout', draft: { ...blankDraft(), roles: ['worker'], tools: ['torque'] } }, ctx(), meta)
  expect(safe.risk).toBe('low')
  expect(safe.status).toBe('proposed')
  const priv = computeLifecyclePlan({ op: 'agent.create', agentId: 'scout', name: 'Scout', draft: { ...blankDraft(), permissionMode: 'bypass', scopes: ['session.write'] } }, ctx(), meta)
  expect(priv.risk).toBe('high')
  expect(priv.status).toBe('invalid')
  expect(priv.issues.some((i) => i.field === 'note')).toBe(true)
  expect(priv.requiredScope).toEqual(expect.arrayContaining(['agent.create', 'agent.write.privileged', 'policy.grant']))
  const dup = computeLifecyclePlan({ op: 'agent.create', agentId: 'designer', name: 'D', draft: blankDraft() }, ctx(), meta)
  expect(dup.issues.some((i) => /already exists/.test(i.message))).toBe(true)
})

it('retire is always high risk and previews impact', () => {
  const worker = MOCK_AGENTS.find((a) => a.identity.id === 'team-worker')!
  const impact = computeRetireImpact(worker, ctx())
  expect(impact.sessions.count).toBe(2)
  expect(impact.tasks.length).toBe(1)
  expect(impact.workstreams.length).toBe(1)
  expect(impact.teams.map((t) => t.teamId).sort()).toEqual(['tm_core', 'tm_gui'])
  const pm = computeRetireImpact(MOCK_AGENTS[0], ctx())
  expect(pm.teams.find((t) => t.teamId === 'tm_core')?.soleCoordinator).toBe(true)
  const plan = computeLifecyclePlan({ op: 'agent.retire', agentId: 'team-worker', sessions: 'stop', note: 'decommissioned' }, ctx(), meta)
  expect(plan.risk).toBe('high')
  expect(plan.status).toBe('proposed')
  expect(computeLifecyclePlan({ op: 'agent.retire', agentId: 'designer', sessions: 'drain' }, ctx(), meta).status).toBe('invalid')
})

it('mock runs create, retire, restore, team and assignment plans through approve/apply and rolls back', async () => {
  const c = createMockAgentsClient()
  const created = await runAll(c, await c.planLifecycle({ op: 'agent.create', agentId: 'scout', name: 'Scout', draft: { ...blankDraft(), roles: ['worker'] } }))
  expect(created.status).toBe('applied')
  expect((await c.getAgent('scout'))?.identity.name).toBe('Scout')

  const retirePlan = await c.planLifecycle({ op: 'agent.retire', agentId: 'team-worker', sessions: 'stop', note: 'done' })
  await expect(c.approvePlan(retirePlan.id, grant('agent.plan.approve', ['agent.write']))).rejects.toThrow(/scope/)
  const retired = await runAll(c, retirePlan)
  expect(retired.status).toBe('applied')
  const w = (await c.getAgent('team-worker'))!
  expect(w.identity.retired).toBe(true)
  expect(w.identity.enabled).toBe(false)
  expect(w.teams).toEqual([])
  expect(w.live?.sessions).toBe(0)
  expect(await c.listAssignments({ agentId: 'team-worker' })).toEqual([])
  expect((await c.getTeam('tm_core'))!.members.some((m) => m.agentId === 'team-worker')).toBe(false)

  const back = await c.rollbackApply(retirePlan.id, grant('agent.plan.rollback', retirePlan.requiredScope))
  expect(back.status).toBe('rolled_back')
  expect((await c.getAgent('team-worker'))!.identity.retired).toBeFalsy()
  expect((await c.listAssignments({ agentId: 'team-worker' })).length).toBe(2)

  const team = (await c.getTeam('tm_gui'))!
  const tp = await c.planLifecycle({ op: 'team.update', teamId: 'tm_gui', team: { ...team, members: [...team.members, { agentId: 'designer', role: 'worker' }] }, baseRevision: team.revision })
  expect((await runAll(c, tp)).status).toBe('applied')
  expect((await c.getAgent('designer'))!.teams.map((t) => t.teamId)).toEqual(['tm_gui'])

  const ap = await c.planLifecycle({ op: 'assignment.update', add: [{ agentId: 'designer', target: { type: 'task', id: 'CW-1' } }], remove: ['asg_3'] })
  expect((await runAll(c, ap)).status).toBe('applied')
  const rows = await c.listAssignments()
  expect(rows.some((r) => r.target.id === 'CW-1')).toBe(true)
  expect(rows.some((r) => r.id === 'asg_3')).toBe(false)

  const dp = await c.planLifecycle({ op: 'team.delete', teamId: 'tm_gui', note: 'obsolete', baseRevision: (await c.getTeam('tm_gui'))!.revision })
  expect(dp.risk).toBe('high')
  await runAll(c, dp)
  expect(await c.getTeam('tm_gui')).toBeNull()
  expect((await c.listAudit('tm_gui')).map((e) => e.kind)).toEqual(expect.arrayContaining(['plan', 'approve', 'apply']))
})

it('reports a conflict when the target changed after planning', async () => {
  const c = createMockAgentsClient()
  const team = (await c.getTeam('tm_core'))!
  const p1 = await c.planLifecycle({ op: 'team.update', teamId: 'tm_core', team: { ...team, summary: 'a' }, baseRevision: team.revision })
  const p2 = await c.planLifecycle({ op: 'team.update', teamId: 'tm_core', team: { ...team, summary: 'b' }, baseRevision: team.revision })
  expect((await runAll(c, p1)).status).toBe('applied')
  expect((await runAll(c, p2)).status).toBe('conflict')
})

it('step-up never completes silently: auto-grant seams are replaced by a manual confirm', () => {
  const bus = createActionBus({ identity: new MockIdentityConfirm() })
  const auto = new MockIdentityConfirm()
  const r = resolveStepUp({ actionBus: bus, identity: auto }, {})
  expect(r.identity).not.toBe(auto)
  expect((r.identity as MockIdentityConfirm).mode).toBe('manual')
  const manual = new MockIdentityConfirm({ mode: 'manual' })
  expect(resolveStepUp({ actionBus: bus, identity: manual }, {}).identity).toBe(manual)
  expect(resolveStepUp({ actionBus: bus, identity: auto }, { allowAutoGrant: true }).identity).toBe(auto)
  const real = { confirm: auto.confirm.bind(auto) }
  const rr = resolveStepUp({ actionBus: bus, identity: real }, {})
  expect(rr.identity).toBe(real)
  expect(rr.bus).toBe(bus)
})

const confirm = async (label: 'Confirm' | 'Cancel') => {
  const dlg = await screen.findByRole('dialog', { name: 'Confirm your identity' })
  await act(async () => { fireEvent.click(within(dlg).getByRole('button', { name: label })) })
}

function mount(seamsIdentity?: MockIdentityConfirm) {
  const client = createMockAgentsClient()
  const identity = new MockIdentityConfirm({ mode: 'manual' })
  const tree = (
    <MemoryRouter>
      <AgentsClientProvider client={client}>
        {seamsIdentity ? (
          <SeamsContext.Provider value={{ identity: seamsIdentity, stateStore: new MemoryStateStore(), actionBus: createActionBus({ identity: seamsIdentity }) }}>
            <AgentDirectoryView now={Date.parse('2026-10-16T05:00:00Z')} />
          </SeamsContext.Provider>
        ) : (
          <AgentEditProvider identity={identity}><AgentDirectoryView now={Date.parse('2026-10-16T05:00:00Z')} /></AgentEditProvider>
        )}
      </AgentsClientProvider>
    </MemoryRouter>
  )
  render(tree)
  return client
}

it('create wizard: clone a privileged agent, reason required, confirm to apply', async () => {
  const client = mount()
  await screen.findByText('Team Reviewer')
  fireEvent.click(screen.getByRole('button', { name: 'New agent' }))
  fireEvent.click(screen.getByRole('radio', { name: /Clone an existing agent/ }))
  fireEvent.change(screen.getByLabelText('Agent to clone'), { target: { value: 'team-worker' } })
  fireEvent.click(screen.getByRole('button', { name: 'Next' }))
  expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('Team Worker copy')
  expect((screen.getByLabelText('Id') as HTMLInputElement).value).toBe('team-worker-copy')
  fireEvent.click(screen.getByRole('button', { name: 'Next' }))
  fireEvent.click(screen.getByRole('button', { name: 'Next' }))
  // bypass permissions copied from the source: high risk, reason required
  expect(screen.getByText('Bypasses permission prompts')).toBeTruthy()
  expect((screen.getByRole('button', { name: 'Review plan' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.change(screen.getByLabelText(/Reason/), { target: { value: 'second worker lane' } })
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Review plan' })) })
  const review = await screen.findByLabelText('Change plan')
  expect(within(review).getByText(/clone of team-worker/)).toBeTruthy()
  await act(async () => { fireEvent.click(within(review).getByRole('button', { name: 'Approve…' })) })
  await confirm('Cancel')
  expect((await screen.findByRole('alert')).textContent).toMatch(/Not confirmed/)
  expect(await client.getAgent('team-worker-copy')).toBeNull()
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Approve…' })) })
  await confirm('Confirm')
  await waitFor(() => expect(screen.getByText(/Approved by Mock User/)).toBeTruthy())
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Apply…' })) })
  await confirm('Confirm')
  await waitFor(() => expect(screen.getByText('Changes applied.')).toBeTruthy())
  expect((await client.getAgent('team-worker-copy'))?.identity.name).toBe('Team Worker copy')
})

it('retire panel shows impact, requires a reason and the step-up (also under auto-grant seams)', async () => {
  const client = mount(new MockIdentityConfirm({ mode: 'auto-grant' }))
  fireEvent.click(within(await screen.findByRole('list', { name: 'Agents' })).getByText('Team Worker'))
  fireEvent.click(screen.getByRole('tab', { name: 'Lifecycle' }))
  const impact = await screen.findByLabelText('Impact')
  expect(within(impact).getByText('sessions').previousSibling?.textContent).toBe('2')
  const btn = screen.getByRole('button', { name: 'Review retire plan' }) as HTMLButtonElement
  expect(btn.disabled).toBe(true)
  fireEvent.change(screen.getByLabelText(/Reason/), { target: { value: 'replaced' } })
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Review retire plan' })) })
  await screen.findByLabelText('Change plan')
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Approve…' })) })
  // auto-grant seams must not approve silently
  await confirm('Confirm')
  await waitFor(() => expect(screen.getByText(/Approved by/)).toBeTruthy())
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Apply…' })) })
  await confirm('Confirm')
  await waitFor(() => expect(screen.getByText('Changes applied.')).toBeTruthy())
  expect((await client.getAgent('team-worker'))?.identity.retired).toBe(true)
})

it('teams and assignments views list data and stage a release as a plan', async () => {
  const client = mount()
  await screen.findByText('Team Reviewer')
  fireEvent.click(screen.getByRole('tab', { name: 'teams' }))
  const members = await screen.findByRole('list', { name: 'Members' })
  expect(within(members).getAllByRole('listitem').length).toBe(2)
  expect(screen.getByText('Shared policy')).toBeTruthy()
  fireEvent.click(screen.getByRole('tab', { name: 'assignments' }))
  await screen.findByRole('list', { name: 'Assignments' })
  fireEvent.click(screen.getByRole('button', { name: 'Release team-reviewer CW-20261016-0121' }))
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: /Review plan/ })) })
  const review = await screen.findByLabelText('Change plan')
  expect(within(review).getByText(/Release team-reviewer from task/)).toBeTruthy()
  void client
})
