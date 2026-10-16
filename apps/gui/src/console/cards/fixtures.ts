// Card fixtures: small hand-written event sets for tests and the preview page.
import type { ConsoleEvent } from '../events/types'

const base = { source: { id: 'fixture' }, extra: {}, actions: [] as ConsoleEvent['actions'] }
const agent = { type: 'agent', id: 'agent-alpha', label: 'agent-alpha' }

export function cardFixtureEvents(t0: number): ConsoleEvent[] {
  return [
    { ...base, id: 'c1', ts: t0 - 50_000, kind: 'agent.state', severity: 'info', subjects: [agent], payload: { state: 'busy' } },
    {
      ...base, id: 'c2', ts: t0 - 40_000, kind: 'approval.requested', severity: 'warning', subjects: [agent, { type: 'task', id: 'CW-1', label: 'CW-1' }], correlationId: 'appr-1',
      actions: [{ id: 'approve', label: 'Approve', kind: 'approve', requiresConfirm: true, scope: ['agent:apply'] }, { id: 'reject', label: 'Reject', kind: 'reject' }],
      payload: { title: 'Apply agent config change', summary: 'Adds the code-review tool.', requestedBy: 'agent-alpha', expiresAt: t0 + 3_600_000 },
    },
    { ...base, id: 'c3', ts: t0 - 30_000, kind: 'message.received', severity: 'notice', subjects: [agent], threadId: 'thr-1', payload: { from: 'agent-alpha', subject: 'Need a decision', preview: 'Proceed with the schema change?' } },
    { ...base, id: 'c4', ts: t0 - 20_000, kind: 'agent.state', severity: 'info', subjects: [agent], payload: { state: 'waiting' } },
  ]
}
