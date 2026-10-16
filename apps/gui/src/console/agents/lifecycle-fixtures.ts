// Demo teams, assignments and templates for the mock AgentsClient. Shapes are
// guesses; see BACKEND_NEEDS.md (B3).

import type { AgentTemplate, AssignmentRecord, TeamRecord } from './lifecycle'

const NOW = Date.parse('2026-10-16T05:00:00Z')

export const MOCK_TEAMS: TeamRecord[] = [
  {
    id: 'tm_core', name: 'Core', summary: 'Standing platform team: coordinates, builds and reviews.',
    members: [{ agentId: 'portfolio-manager', role: 'coordinator' }, { agentId: 'team-worker', role: 'worker' }, { agentId: 'team-reviewer', role: 'reviewer' }],
    policy: { permissionCeiling: 'bypass', scopesCeiling: ['message.write'], rules: ['no-secrets', 'branch-pr'], reportTo: 'portfolio-manager' },
    revision: 't1a', updatedAt: NOW - 5 * 86400_000,
  },
  {
    id: 'tm_gui', name: 'Console GUI', summary: 'Builds the agent console interface.',
    members: [{ agentId: 'team-worker', role: 'worker' }, { agentId: 'lead-proxy', role: 'proxy' }],
    policy: { permissionCeiling: 'default', scopesCeiling: ['message.write'], rules: ['no-secrets', 'no-main-push'] },
    revision: 't2b', updatedAt: NOW - 86400_000,
  },
]

export const MOCK_ASSIGNMENTS: AssignmentRecord[] = [
  { id: 'asg_1', agentId: 'team-worker', target: { type: 'task', id: 'CW-20261016-0123', label: 'B3 Agent manager lifecycle' }, role: 'implementer', since: NOW - 2 * 3600_000 },
  { id: 'asg_2', agentId: 'team-worker', target: { type: 'workstream', id: 'ws-agent-console', label: 'Agent Console GUI' }, role: 'worker', since: NOW - 86400_000 },
  { id: 'asg_3', agentId: 'team-reviewer', target: { type: 'task', id: 'CW-20261016-0121', label: 'G3 PR workspace' }, role: 'reviewer', since: NOW - 3600_000 },
  { id: 'asg_4', agentId: 'portfolio-manager', target: { type: 'workstream', id: 'ws-agent-console', label: 'Agent Console GUI' }, role: 'coordinator', since: NOW - 2 * 86400_000 },
  { id: 'asg_5', agentId: 'lead-proxy', target: { type: 'workstream', id: 'ws-agent-console', label: 'Agent Console GUI' }, role: 'proxy', since: NOW - 2 * 86400_000 },
]

export const MOCK_TEMPLATES: AgentTemplate[] = [
  { id: 'tpl-worker', name: 'Worker', summary: 'Does one task in its own worktree and opens a PR.', draft: { roles: ['worker'], workspaceMode: 'worktree', tools: ['torque', 'tesseract'], skills: ['search-first'], rules: ['no-secrets', 'no-main-push', 'branch-pr', 'report-to-coordinator'] } },
  { id: 'tpl-reviewer', name: 'Reviewer', summary: 'Read-only. Reviews a diff and ranks findings.', draft: { roles: ['reviewer'], workspaceMode: 'worktree', tools: ['torque', 'tesseract'], skills: ['search-first'], rules: ['no-secrets', 'read-only', 'no-self-approve'] } },
  { id: 'tpl-partner', name: 'Partner', summary: 'Standing conversation partner. Reads, drafts, recommends.', draft: { roles: ['partner'], workspaceMode: 'hybrid', network: true, tools: ['torque', 'tesseract', 'tangent'], skills: ['search-first', 'capture-decision'], scopes: ['message.write'], rules: ['no-secrets', 'ask-outward', 'not-implementer'] } },
  { id: 'tpl-operator', name: 'Operator (privileged)', summary: 'Runs releases and deploys. Bypasses prompts; needs a written reason.', draft: { roles: ['coordinator'], workspaceMode: 'worktree', network: true, permissionMode: 'bypass', sandbox: 'unrestricted', tools: ['torque', 'tesseract', 'cerberus'], scopes: ['session.write'], rules: ['no-secrets', 'ask-outward'] } },
]
