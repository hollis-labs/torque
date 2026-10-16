import type { AlertRulePack } from './types'

/**
 * Default rule pack. Plain JSON-able data (see parseRulePack). Kinds marked
 * "placeholder" await the data team's catalog (C2); the swap is an edit here.
 * Order matters: first match wins, so specific rules precede the catch-alls.
 */
export const DEFAULT_RULE_PACK: AlertRulePack = {
  version: 1,
  id: 'console.default',
  label: 'Console default rules',
  rules: [
    { id: 'drop-metrics', description: 'Metric ticks are never alerts.', match: { kinds: ['metric.tick'] }, action: 'drop' },
    { id: 'drop-alert-cleared', description: 'A cleared alert is not a new one.', match: { kinds: ['alert.cleared'] }, action: 'drop' },
    {
      id: 'agent-waiting',
      description: 'An agent is blocked waiting on a permission or an answer.',
      match: { kinds: ['agent.state'], payload: { state: 'waiting' } },
      alert: { severity: 'warning', icon: 'agent', title: '{{agent,subject.label}} is waiting', detail: '{{payload.waitingFor|Waiting on you}}' },
      coalesce: { key: 'waiting:{{agent}}' },
      escalate: { afterSec: 600, to: 'error' },
    },
    { id: 'agent-not-waiting', match: { kinds: ['agent.state'] }, action: 'drop' },
    {
      id: 'approval-requested',
      description: 'A decision is needed (go-hitl approval).',
      match: { kinds: ['approval.requested'] },
      alert: { severity: 'warning', icon: 'approval', title: '{{payload.title|Approval requested}}', detail: '{{payload.summary}}' },
      escalate: { afterSec: 900, to: 'error' },
    },
    {
      id: 'approval-resolved',
      match: { kinds: ['approval.resolved'] },
      alert: { severity: 'info', icon: 'approval', title: '{{payload.title|Approval resolved}}', detail: '{{payload.summary}}' },
    },
    {
      id: 'task-failed',
      description: 'Placeholder: task.state_changed to a failed state.',
      match: { kinds: ['task.state_changed'], payload: { to: { in: ['failed', 'error', 'errored'] } } },
      alert: { severity: 'error', icon: 'task', title: '{{subject.label}} failed', detail: '{{payload.title}}' },
      coalesce: { key: 'task:{{subject.id}}' },
    },
    {
      id: 'task-needs-review',
      match: { kinds: ['task.state_changed'], payload: { to: { in: ['review', 'needs_review', 'blocked'] } } },
      alert: { severity: 'notice', icon: 'task', title: '{{subject.label}} → {{payload.to}}', detail: '{{payload.title}}' },
      coalesce: { key: 'task:{{subject.id}}' },
    },
    {
      id: 'task-state',
      description: 'Other task transitions are tray-only.',
      match: { kinds: ['task.state_changed'] },
      alert: { severity: 'info', icon: 'task', title: '{{subject.label}} → {{payload.to|updated}}', detail: '{{payload.title}}' },
      coalesce: { key: 'task:{{subject.id}}' },
    },
    {
      id: 'message-received',
      description: 'A new tether message; bursts from one sender fold.',
      match: { kinds: ['message.received'] },
      alert: { severity: 'notice', icon: 'message', title: '{{payload.subject|New message}}', detail: '{{payload.from}}: {{payload.preview}}' },
      coalesce: { key: 'msg:{{payload.from}}' },
    },
    { id: 'message-lifecycle', description: 'Delivery bookkeeping is not an alert unless it failed.', match: { kinds: ['message.failed', 'message.dead_lettered'] }, alert: { severity: 'warning', icon: 'message', title: 'Message {{kind}}', detail: '{{payload.subject}}' }, coalesce: { key: 'msgfail:{{payload.from}}' } },
    { id: 'message-lifecycle-drop', match: { kinds: ['message.*'] }, action: 'drop' },
    {
      id: 'pr-review-requested',
      match: { kinds: ['pr.review_requested'] },
      alert: { severity: 'notice', icon: 'pr', title: '{{payload.repo|PR}}#{{payload.number}} needs review', detail: '{{payload.title}}' },
      coalesce: { key: 'pr:{{payload.repo}}#{{payload.number}}' },
    },
    {
      id: 'pr-checks-failed',
      match: { kinds: ['pr.checks_failed'] },
      alert: { severity: 'error', icon: 'pr', title: '{{payload.repo|PR}}#{{payload.number}} checks failed', detail: '{{payload.title}}' },
      coalesce: { key: 'pr:{{payload.repo}}#{{payload.number}}' },
    },
    {
      id: 'pr-other',
      match: { kinds: ['pr.*'] },
      alert: { severity: 'info', icon: 'pr', title: '{{payload.repo|PR}}#{{payload.number}} {{kind}}', detail: '{{payload.title}}' },
      coalesce: { key: 'pr:{{payload.repo}}#{{payload.number}}' },
    },
    {
      id: 'budget-exceeded',
      description: 'Placeholder kind. payload.percent is the share of budget used.',
      match: { kinds: ['budget.threshold', 'budget.exceeded'], payload: { percent: { gte: 100 } } },
      alert: { severity: 'critical', icon: 'error', title: 'Budget exceeded: {{payload.scope,subject.label}}', detail: '{{payload.percent}}% used' },
      coalesce: { key: 'budget:{{payload.scope}}' },
    },
    {
      id: 'budget-threshold',
      match: { kinds: ['budget.threshold'], payload: { percent: { gte: 80 } } },
      alert: { severity: 'warning', icon: 'warning', title: 'Budget at {{payload.percent}}%: {{payload.scope,subject.label}}', detail: '{{payload.detail}}' },
      coalesce: { key: 'budget:{{payload.scope}}' },
    },
    {
      id: 'checkpoint-pending',
      description: 'Placeholder kinds for a checkpoint awaiting a response.',
      match: { kinds: ['checkpoint.pending', 'checkpoint.requested'] },
      alert: { severity: 'warning', icon: 'approval', title: 'Checkpoint pending: {{subject.label}}', detail: '{{payload.question,payload.title}}' },
      escalate: { afterSec: 900, to: 'error' },
    },
    {
      id: 'checkpoint-state',
      match: { kinds: ['checkpoint.state_changed'], payload: { to: 'pending' } },
      alert: { severity: 'warning', icon: 'approval', title: 'Checkpoint pending: {{subject.label}}', detail: '{{payload.title}}' },
      escalate: { afterSec: 900, to: 'error' },
    },
    {
      id: 'fleet-node-down',
      match: { kinds: ['fleet.node.down', 'fleet.node.degraded'] },
      alert: { severity: 'error', icon: 'error', title: '{{subject.label,source}} {{kind}}', detail: '{{payload.reason}}' },
      coalesce: { key: 'fleet:{{subject.id}}' },
    },
    {
      id: 'alert-raised',
      description: 'Producer-classified alert (alert.raised): trust its severity.',
      match: { kinds: ['alert.raised'] },
      alert: { severity: 'inherit', title: '{{payload.title,subject.label}}', detail: '{{payload.detail}}' },
      coalesce: { key: 'raised:{{subject.id}}' },
    },
    {
      id: 'classified-alert',
      description: 'Envelope v1.1: anything the producer classed as an alert, or labelled needs-attention.',
      match: { categories: ['alert'], minSeverity: 'notice' },
      alert: { severity: 'inherit', title: '{{kind}}', detail: '{{subject.label}}' },
    },
    {
      id: 'catch-all',
      description: 'Unknown kinds degrade to a generic alert at notice and above.',
      match: { minSeverity: 'notice' },
      alert: { severity: 'inherit', title: '{{kind}}', detail: '{{subject.label}}' },
    },
  ],
}
