// Workflow fixtures: runs in each interesting state, for tests and the preview.
import type { WorkflowRun } from './types'

const MIN = 60_000
const agent = { id: 'agent-alpha', label: 'agent-alpha' }

export function workflowFixtures(t0: number): WorkflowRun[] {
  const a = (at: number, actor: string, action: string, note?: string) => ({ at, actor, action, ...(note ? { note } : {}) })
  return [
    {
      id: 'run-1', title: 'Migrate planner schema', state: 'running', priority: 'normal', agent,
      phases: [
        { id: 'p1', label: 'Design', status: 'passed', startedAt: t0 - 50 * MIN, endedAt: t0 - 30 * MIN, gate: { id: 'g1', label: 'Design review', status: 'passed', requiresHuman: true, criteria: [{ label: 'Schema doc reviewed', met: true }] } },
        { id: 'p2', label: 'Implement', status: 'active', startedAt: t0 - 30 * MIN, progress: { done: 3, total: 8 } },
        { id: 'p3', label: 'Verify', status: 'pending', gate: { id: 'g3', label: 'Release gate', status: 'open', requiresHuman: true, evaluatedAt: t0 - 2 * MIN, criteria: [{ label: 'Tests green', met: true }, { label: 'Migration dry-run clean', met: true }, { label: 'Rollback documented', met: false }] } },
        { id: 'p4', label: 'Ship', status: 'pending' },
      ],
      subjects: [{ type: 'run', id: 'run-1', label: 'Migrate planner schema' }, { type: 'task', id: 'CW-20261016-0120', label: 'CW-20261016-0120' }],
      audit: [a(t0 - 50 * MIN, 'agent-alpha', 'started')], createdAt: t0 - 50 * MIN, updatedAt: t0 - 2 * MIN,
    },
    {
      id: 'run-2', title: 'Refactor messaging client', state: 'awaiting_plan', priority: 'high', agent: { id: 'agent-beta', label: 'agent-beta' },
      phases: [],
      plan: {
        status: 'pending', proposedAt: t0 - 6 * MIN,
        items: [
          { id: 'i1', label: 'Extract transport interface', summary: 'Small, reversible.', included: true },
          { id: 'i2', label: 'Port consumers', summary: 'Touches 14 files.', included: true },
          { id: 'i3', label: 'Delete legacy path', summary: 'Irreversible; needs a release first.', included: true },
        ],
      },
      subjects: [{ type: 'run', id: 'run-2', label: 'Refactor messaging client' }], audit: [a(t0 - 6 * MIN, 'agent-beta', 'plan:proposed')], createdAt: t0 - 8 * MIN, updatedAt: t0 - 6 * MIN,
    },
    {
      id: 'run-3', title: 'Nightly dependency audit', state: 'blocked', priority: 'normal', agent: { id: 'agent-gamma', label: 'agent-gamma' },
      blockedReason: 'Needs a decision on the major version bump',
      phases: [{ id: 'p1', label: 'Scan', status: 'passed' }, { id: 'p2', label: 'Upgrade', status: 'active', progress: { done: 1, total: 4 } }],
      checkpoint: {
        id: 'cp-1', question: 'Upgrade react-router to v8?', allowText: true, status: 'pending', emittedAt: t0 - 9 * MIN,
        options: [{ value: 'yes', label: 'Yes, upgrade now', description: 'Breaking changes in data routers.' }, { value: 'defer', label: 'Defer to next sprint' }],
      },
      subjects: [{ type: 'run', id: 'run-3', label: 'Nightly dependency audit' }], audit: [a(t0 - 9 * MIN, 'agent-gamma', 'checkpoint:emitted')], createdAt: t0 - 40 * MIN, updatedAt: t0 - 9 * MIN,
    },
    {
      id: 'run-4', title: 'Docs sweep', state: 'completed', priority: 'low', agent,
      phases: [{ id: 'p1', label: 'Sweep', status: 'passed' }, { id: 'p2', label: 'Publish', status: 'passed' }],
      subjects: [{ type: 'run', id: 'run-4', label: 'Docs sweep' }], audit: [], createdAt: t0 - 3 * 3_600_000, updatedAt: t0 - 2 * 3_600_000,
    },
  ]
}
