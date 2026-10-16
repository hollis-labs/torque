// HITL fixtures: one of each kind plus closed/expired states, for tests and the preview.
import type { HitlRequest } from './types'

const MIN = 60_000
const agent = { id: 'agent-alpha', label: 'agent-alpha', kind: 'agent' }

export function hitlFixtures(t0: number): HitlRequest[] {
  return [
    {
      id: 'hitl-appr-1', type: 'approval', title: 'Apply agent config change', prompt: 'agent-alpha wants to add the code-review tool and raise its token budget.',
      requester: agent, risk: 'high', createdAt: t0 - 4 * MIN, expiresAt: t0 + 42 * MIN, status: 'pending',
      scope: ['agent:apply'], effects: ['Adds tool: code-review', 'Token budget 200k -> 400k'], allowDefer: true,
      contextLinks: [{ type: 'task', id: 'CW-20261016-0119', label: 'CW-20261016-0119' }, { type: 'pr', id: 'hollis-labs/torque#271', label: 'torque#271', url: 'https://github.com/hollis-labs/torque/pull/271' }],
      audit: [{ at: t0 - 4 * MIN, actor: 'agent-alpha', action: 'requested' }], tangentKind: 'tangent.hitl-item', correlationId: 'appr-1',
    },
    {
      id: 'hitl-q-1', type: 'question', mode: 'choice', title: 'Which migration strategy?', prompt: 'The schema change touches 3 tables. How should I roll it out?',
      requester: { id: 'agent-beta', label: 'agent-beta', kind: 'agent' }, risk: 'medium', createdAt: t0 - 9 * MIN, expiresAt: t0 + 3 * 3_600_000, status: 'pending', allowOther: true,
      options: [
        { value: 'expand', label: 'Expand then contract', description: 'Two deploys, no downtime.' },
        { value: 'bigbang', label: 'Single migration', description: 'One deploy, brief write lock.' },
      ],
      contextLinks: [{ type: 'task', id: 'CW-20261016-0110', label: 'CW-20261016-0110' }],
      audit: [{ at: t0 - 9 * MIN, actor: 'agent-beta', action: 'requested' }], tangentKind: 'tangent.interview-question',
    },
    {
      id: 'hitl-q-2', type: 'question', mode: 'text', title: 'Name for the new workstream?', requester: { id: 'agent-beta', label: 'agent-beta', kind: 'agent' }, risk: 'low',
      createdAt: t0 - 20 * MIN, status: 'pending', options: [], placeholder: 'e.g. ws-agent-console', contextLinks: [], audit: [{ at: t0 - 20 * MIN, actor: 'agent-beta', action: 'requested' }],
    },
    {
      id: 'hitl-sec-1', type: 'secret', title: 'GitHub token needed', prompt: 'The forge plugin needs a token to open the pull request.',
      requester: { id: 'wf-release', label: 'release workflow', kind: 'workflow' }, risk: 'critical', createdAt: t0 - 2 * MIN, expiresAt: t0 + 4 * MIN + 30_000, status: 'pending',
      fieldLabel: 'Personal access token', purpose: 'Create a pull request on hollis-labs/torque', destination: 'cerberus vault: github/token',
      contextLinks: [{ type: 'run', id: 'run-4821', label: 'release run 4821' }], audit: [{ at: t0 - 2 * MIN, actor: 'wf-release', action: 'requested' }],
    },
    {
      id: 'hitl-form-1', type: 'form', title: 'Launch parameters', prompt: 'Fill these in to start the nightly review agent.',
      requester: { id: 'agent-gamma', label: 'agent-gamma', kind: 'agent' }, risk: 'low', createdAt: t0 - 30 * MIN, expiresAt: t0 + 6 * 3_600_000, status: 'pending', submitLabel: 'Launch',
      fields: [
        { id: 'repo', label: 'Repository', kind: 'text', required: true, placeholder: 'owner/name' },
        { id: 'depth', label: 'Review depth', kind: 'select', required: true, options: [{ value: 'quick', label: 'Quick' }, { value: 'deep', label: 'Deep' }] },
        { id: 'budget', label: 'Token budget', kind: 'number', default: 100000, help: 'Hard cap for this run.' },
        { id: 'notes', label: 'Notes', kind: 'textarea' },
        { id: 'ack', label: 'I have read the review policy', kind: 'checkbox', required: true },
      ],
      contextLinks: [], audit: [{ at: t0 - 30 * MIN, actor: 'agent-gamma', action: 'requested' }], tangentKind: 'tangent.form-collect',
    },
    {
      id: 'hitl-appr-done', type: 'approval', title: 'Delete stale worktree', requester: agent, risk: 'medium', createdAt: t0 - 3 * 3_600_000, status: 'answered', scope: ['worktree:delete'],
      contextLinks: [], outcome: { label: 'Approved', at: t0 - 2 * 3_600_000, by: 'chrispian' },
      audit: [
        { at: t0 - 3 * 3_600_000, actor: 'agent-alpha', action: 'requested' },
        { at: t0 - 2 * 3_600_000, actor: 'chrispian', action: 'confirmed', note: 'identity step-up: hitl:approve, worktree:delete' },
        { at: t0 - 2 * 3_600_000, actor: 'chrispian', action: 'approved', note: 'ok to remove' },
      ],
    },
    {
      id: 'hitl-appr-exp', type: 'approval', title: 'Force-push release branch', requester: agent, risk: 'critical', createdAt: t0 - 2 * 3_600_000, expiresAt: t0 - MIN, status: 'pending', scope: ['git:force-push'],
      contextLinks: [], audit: [{ at: t0 - 2 * 3_600_000, actor: 'agent-alpha', action: 'requested' }],
    },
  ]
}
