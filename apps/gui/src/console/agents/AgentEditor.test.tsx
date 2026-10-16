/** @vitest-environment jsdom */
import { afterEach, expect, it } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AgentDirectoryView } from './AgentDirectory'
import { AgentsClientProvider } from './context'
import { AgentEditProvider } from './edit-context'
import { createMockAgentsClient, MOCK_AGENTS } from './mock'
import { assessRisks, computePlan, diffDraft, draftFromAgent, maxRisk, validateDraft } from './plan'
import { createActionBus, MockIdentityConfirm } from '../seams'
import type { ConfirmResult } from '../seams'

afterEach(cleanup)

const reviewer = () => MOCK_AGENTS.find((a) => a.identity.id === 'team-reviewer')!
const grant = (action: string, scope: string[]): ConfirmResult => ({ outcome: 'granted', who: { id: 'u' }, what: { action, title: action }, scope, issuedAt: Date.now(), expiresAt: Date.now() + 60_000, token: 't' })

it('diffs list and scalar fields and flags privileged changes', () => {
  const before = draftFromAgent(reviewer())
  const after = { ...before, permissionMode: 'bypass', scopes: ['session.write'], rules: before.rules.filter((r) => r !== 'no-secrets'), network: true }
  const changes = diffDraft(before, after)
  expect(changes.map((c) => c.field).sort()).toEqual(['network', 'permissionMode', 'rules', 'scopes'])
  const risks = assessRisks(before, after)
  expect(maxRisk(risks)).toBe('high')
  expect(risks.map((r) => r.id)).toEqual(expect.arrayContaining(['perm-bypass', 'scope-session.write', 'rule-no-secrets', 'network-on']))
  expect(diffDraft(before, before)).toEqual([])
})

it('validates drafts', () => {
  const d = draftFromAgent(reviewer())
  expect(validateDraft({ ...d, provider: '' }).some((i) => i.field === 'provider' && i.level === 'error')).toBe(true)
  expect(validateDraft({ ...d, scopes: ['bad'] }).some((i) => i.field === 'scopes')).toBe(true)
  expect(validateDraft({ ...d, scopes: ['session.write'] }).some((i) => i.message.includes('read-only'))).toBe(true)
  expect(validateDraft(d, { risk: 'high', note: '' }).some((i) => i.field === 'note')).toBe(true)
  const plan = computePlan(reviewer(), { agentId: 'team-reviewer', draft: d }, { id: 'p', now: 1, actor: { id: 'u' } })
  expect(plan.status).toBe('invalid')
})

it('mock client enforces plan -> approve -> apply with step-up, then rolls back', async () => {
  const c = createMockAgentsClient()
  const draft = { ...draftFromAgent(reviewer()), network: true }
  const plan = await c.planChange({ agentId: 'team-reviewer', draft, baseRevision: '77bd0a1' })
  expect(plan.status).toBe('proposed')
  await expect(c.approvePlan(plan.id, { ...grant('agent.plan.approve', plan.requiredScope), outcome: 'denied' })).rejects.toThrow(/not granted/)
  await expect(c.approvePlan(plan.id, grant('agent.plan.apply', plan.requiredScope))).rejects.toThrow(/not "agent.plan.approve"|for "agent.plan.apply"/)
  await expect(c.applyPlan(plan.id, grant('agent.plan.apply', plan.requiredScope))).rejects.toThrow(/approve it first/)
  const approved = await c.approvePlan(plan.id, grant('agent.plan.approve', plan.requiredScope))
  expect(approved.status).toBe('approved')
  const out = await c.applyPlan(plan.id, grant('agent.plan.apply', plan.requiredScope))
  expect(out.status).toBe('applied')
  expect((await c.getAgent('team-reviewer'))!.policy.network).toBe(true)
  expect(reviewer().policy.network).toBe(false)
  const back = await c.rollbackApply(plan.id, grant('agent.plan.rollback', plan.requiredScope))
  expect(back.status).toBe('rolled_back')
  expect((await c.getAgent('team-reviewer'))!.policy.network).toBe(false)
  expect((await c.listAudit('team-reviewer')).map((e) => e.kind).sort()).toEqual(['apply', 'approve', 'plan', 'rollback'])
})

it('mock client reports conflict, failure and expiry without changing the agent', async () => {
  let t = 1_000
  const c = createMockAgentsClient(undefined, 0, { now: () => t, conflictFor: ['designer'], failApplyFor: ['lead-proxy'], approvalTtlMs: 100 })
  const flow = async (id: string, base: string) => {
    const a = (await c.getAgent(id))!
    const plan = await c.planChange({ agentId: id, draft: { ...draftFromAgent(a), network: !a.policy.network }, baseRevision: base })
    await c.approvePlan(plan.id, grant('agent.plan.approve', plan.requiredScope))
    return plan
  }
  const p1 = await flow('designer', '12fe8c0')
  expect((await c.applyPlan(p1.id, grant('agent.plan.apply', p1.requiredScope))).status).toBe('conflict')
  const p2 = await flow('lead-proxy', '')
  const f = await c.applyPlan(p2.id, grant('agent.plan.apply', p2.requiredScope))
  expect(f.status).toBe('failed')
  expect((await c.getAgent('lead-proxy'))!.policy.network).toBeUndefined()
  const p3 = await flow('team-worker', 'a41c9e2')
  t += 500
  expect((await c.applyPlan(p3.id, grant('agent.plan.apply', p3.requiredScope))).status).toBe('expired')
})

function mount(identity: MockIdentityConfirm) {
  const client = createMockAgentsClient()
  const bus = createActionBus({ identity })
  render(
    <MemoryRouter>
      <AgentsClientProvider client={client}>
        <AgentEditProvider bus={bus} identity={identity}><AgentDirectoryView now={Date.parse('2026-10-16T05:00:00Z')} /></AgentEditProvider>
      </AgentsClientProvider>
    </MemoryRouter>,
  )
  return client
}

const confirmDialog = async (label: 'Confirm' | 'Cancel') => {
  const dlg = await screen.findByRole('dialog', { name: 'Confirm your identity' })
  expect(within(dlg).getByText('Who')).toBeTruthy()
  expect(within(dlg).getByText('Scope')).toBeTruthy()
  await act(async () => { fireEvent.click(within(dlg).getByRole('button', { name: label })) })
}

it('edits, reviews, approves and applies through the step-up dialog, then rolls back', async () => {
  const identity = new MockIdentityConfirm({ mode: 'manual' })
  const client = mount(identity)
  fireEvent.click(await screen.findByText('Team Reviewer'))
  fireEvent.click(screen.getByRole('tab', { name: 'Edit' }))
  fireEvent.click(screen.getByRole('button', { name: '+ session.write' }))
  // read-only + write scope is invalid, and high risk needs a reason
  expect(screen.getByText(/contradicts write scopes/)).toBeTruthy()
  expect((screen.getByRole('button', { name: 'Review plan' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.click(screen.getByRole('button', { name: 'read-only' }))
  fireEvent.change(screen.getByLabelText(/Reason/), { target: { value: 'needs to open sessions' } })
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Review plan' })) })
  const review = await screen.findByLabelText('Change plan')
  expect(within(review).getByText('Grants privileged scope session.write')).toBeTruthy()
  expect(within(review).getByText('+ session.write')).toBeTruthy()
  expect(within(review).getByText('PROPOSED', { exact: false })).toBeTruthy()

  // cancelling the step-up changes nothing
  await act(async () => { fireEvent.click(within(review).getByRole('button', { name: 'Approve…' })) })
  await confirmDialog('Cancel')
  expect((await screen.findByRole('alert')).textContent).toMatch(/Not confirmed/)
  expect((await client.getAgent('team-reviewer'))!.policy.scopes).toEqual([])

  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Approve…' })) })
  await confirmDialog('Confirm')
  await waitFor(() => expect(screen.getByText(/Approved by Mock User/)).toBeTruthy())
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Apply…' })) })
  await confirmDialog('Confirm')
  await waitFor(() => expect(screen.getByText('Changes applied.')).toBeTruthy())
  expect((await client.getAgent('team-reviewer'))!.policy.scopes).toEqual(['session.write'])

  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Roll back…' })) })
  await confirmDialog('Confirm')
  await waitFor(() => expect(screen.getByText(/Changes rolled back/)).toBeTruthy())
  expect((await client.getAgent('team-reviewer'))!.policy.scopes).toEqual([])

  fireEvent.click(screen.getByRole('button', { name: 'Done' }))
  fireEvent.click(screen.getByRole('tab', { name: 'Audit' }))
  const trail = await screen.findByRole('list', { name: 'Audit trail' })
  expect(within(trail).getAllByRole('listitem').length).toBeGreaterThanOrEqual(4)
})
