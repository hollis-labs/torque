// Representative samples of the DRAFT event/message stream kind families
// (event_message_stream_contract_2026_10_16), built with envelope v1.1 blocks.
// Everything here is a placeholder shape for UI development; field names track
// the draft and may change. Message events carry `body_ref`, NEVER a body.

import { KINDS, MESSAGE_KINDS, type ConsoleActionOffer, type ConsoleEvent, type ConsoleSeverity } from './types'

export const FIXTURE_EPOCH = 'fx-epoch-1'

type Sample = Pick<ConsoleEvent, 'kind' | 'payload'> & Partial<Omit<ConsoleEvent, 'kind' | 'payload'>> & { category: string }

const task = { type: 'task', id: 'CW-20261016-0100', label: 'CW-20261016-0100' }
const run = { type: 'run', id: 'RUN-1', label: 'RUN-1' }
const agent = { type: 'agent', id: 'agent-alpha', label: 'agent-alpha' }

function messageSamples(): Sample[] {
  return MESSAGE_KINDS.map((kind, i) => ({
    kind,
    category: 'message',
    subjects: [{ type: 'message', id: 'msg-1' }],
    threadId: 'thr-agent-alpha',
    severity: (kind.endsWith('failed') || kind.endsWith('dead_lettered') ? 'error' : 'info') as ConsoleSeverity,
    payload: {
      message_id: 'msg-1',
      thread_id: 'thr-agent-alpha',
      from: 'agent-alpha',
      to: ['chrispian'],
      msg_kind: 'request',
      urgency: 'normal',
      interrupt: false,
      delivery: { state: kind.split('.').pop(), attempt: 1 + (i % 2), binding_generation: 1 },
      body_ref: 'mailbox://msg-1',
      body_bytes: 482,
      content_type: 'text/markdown',
    },
  }))
}

function samples(): Sample[] {
  const stateChanged = (entity: string, from: string, to: string, extra: Partial<Sample> = {}): Sample => ({
    kind: `${entity}.state_changed`,
    category: 'state',
    subjects: [{ type: entity, id: `${entity}-1` }],
    payload: { entity: { type: entity, id: `${entity}-1` }, from, to, trigger: { kind: 'agent', ref: 'agent-alpha' }, reason: 'sample' },
    ...extra,
  })
  const offers: ConsoleActionOffer[] = [
    { id: 'approve', label: 'Approve', kind: 'approve', requiresConfirm: true, scope: ['approval:resolve'] },
    { id: 'deny', label: 'Deny', kind: 'reject' },
  ]
  return [
    stateChanged('task', 'in_progress', 'review'),
    stateChanged('run', 'running', 'failed', { severity: 'error' }),
    stateChanged('session', 'busy', 'idle'),
    stateChanged('checkpoint', 'pending', 'responded'),
    stateChanged('sprint', 'planned', 'active'),
    stateChanged('phase', 'pending', 'active'),
    stateChanged('claim', 'open', 'claimed'),
    stateChanged('delivery', 'queued', 'delivered'),
    { kind: KINDS.gateEvaluated, category: 'state', subjects: [task], severity: 'warning',
      payload: { gate: { id: 'g-ci', kind: 'ci_green' }, subject: { type: 'task', id: task.id }, result: 'blocked', blocking: true, evidence_ref: 'artifact://ci/123' } },
    { kind: KINDS.phaseStarted, category: 'state', subjects: [{ type: 'phase', id: 'ph-2' }],
      payload: { plan_id: 'plan-1', phase_id: 'ph-2', name: 'Wave 1', order: 2, progress: { done: 0, total: 6 } } },
    { kind: KINDS.phaseCompleted, category: 'state', subjects: [{ type: 'phase', id: 'ph-1' }],
      payload: { plan_id: 'plan-1', phase_id: 'ph-1', name: 'Wave 0', order: 1, progress: { done: 4, total: 4 } } },
    { kind: KINDS.progressCheckpoint, category: 'progress', subjects: [task],
      payload: { detail_level: 'step', label: 'Adapter layer', pct: 60, step: 3, total_steps: 5, summary: 'Store and adapters done; tests next.' } },
    { kind: KINDS.dataEmitted, category: 'data', subjects: [task], severity: 'debug',
      payload: { schema: 'burndown@1', data: { done: 3, total: 8 } } },
    { kind: KINDS.stepExecuted, category: 'step', subjects: [task], severity: 'debug',
      payload: { step_id: 'st-1', rule_id: 'rule-review-ping', trigger_event_id: 'fx-1', input_digest: 'sha256:abc', outcome: 'skipped', effects: [] },
      corr: { causationId: 'fx-1', depth: 1 } },
    { kind: KINDS.toolCallStarted, category: 'tool', subjects: [agent], severity: 'debug',
      payload: { tool: 'torque_task_get', server: 'tether', args_bytes: 64, args_schema_fp: 'fp1' } },
    { kind: KINDS.toolCallFinished, category: 'tool', subjects: [agent], severity: 'debug',
      payload: { tool: 'torque_task_get', server: 'tether', ok: true, duration_ms: 42, gateway_ms: 3, forward_ms: 38, result_bytes: 2048 } },
    ...messageSamples(),
    { kind: KINDS.approvalRequested, category: 'approval', subjects: [agent, task], severity: 'warning', actions: offers,
      payload: { request_id: 'req-1', class: 'agent.apply', requester: 'agent-alpha', subject: { type: 'task', id: task.id }, expires_at: 0, hitl_ref: 'hitl://req-1' } },
    { kind: KINDS.approvalResolved, category: 'approval', subjects: [agent, task],
      payload: { request_id: 'req-1', outcome: 'approved', resolver: { kind: 'operator', id: 'chrispian' }, hitl_ref: 'hitl://req-1' } },
    ...(['down', 'up', 'restarted', 'degraded'] as const).map((v): Sample => ({
      kind: `fleet.node.${v}`, category: 'fleet', subjects: [{ type: 'node', id: 'worker-hub', label: 'worker-hub' }],
      severity: v === 'down' ? 'error' : v === 'degraded' ? 'warning' : 'notice',
      payload: { environment_id: 'env-1', authority: 'hub', worker_id: 'worker-hub', reason: v === 'down' ? 'heartbeat lost' : 'sample', last_seen: 0 },
    })),
    { kind: KINDS.eventClassified, category: 'alert', subjects: [task], severity: 'info',
      payload: { rule_id: 'rule-ci', source_event_ids: ['fx-1'], labels: ['ci'], summary: 'CI classified', confidence: 'derived' } },
    { kind: KINDS.alertRaised, category: 'alert', subjects: [task], severity: 'error', threadId: 'alert-ci-fail',
      actions: [{ id: 'open', label: 'Open task', kind: 'navigate', params: { to: `/tasks/${task.id}` } }],
      payload: { rule_id: 'rule-ci', source_event_ids: ['fx-1'], labels: ['ci'], coalesce_key: 'ci-fail:task', summary: 'CI failing on 3 PRs', confidence: 'derived' } },
    { kind: KINDS.alertCleared, category: 'alert', subjects: [task], severity: 'info', threadId: 'alert-ci-fail',
      payload: { rule_id: 'rule-ci', coalesce_key: 'ci-fail:task', summary: 'CI recovered' } },
  ]
}

/** One event per sample, id `fx-fam-N`, stamped with v1.1 blocks. `baseTs` is the first timestamp; later ones follow 1s apart. */
export function familyEvents(baseTs: number, cursorBase = 0): ConsoleEvent[] {
  return samples().map((s, i) => {
    const { category, ...rest } = s
    const e: ConsoleEvent = {
      id: `fx-fam-${i + 1}`,
      ts: baseTs + i * 1000,
      source: { id: 'fixture', machine: 'hub' },
      subjects: [],
      severity: 'info',
      actions: [],
      extra: {},
      ...rest,
    }
    e.scope = { environment_id: 'env-1', project_id: 'PRJ-20260416-0001', task_id: task.id, run_id: run.id, worker_id: 'worker-hub' }
    e.corr = { traceId: `tr-${i + 1}`, ...(rest.corr ?? {}), ...(rest.threadId ? { threadId: rest.threadId } : {}) }
    e.actor = { kind: category === 'fleet' ? 'system' : 'agent', id: 'urn:agent:alpha', verified: 'claimed' }
    e.class = { category, severity: e.severity, sensitivity: 'private', visibility: 'environment', payloadSchema: `${e.kind}@1`, sanitized: true, redactions: 0 }
    e.recordedAt = e.ts
    e.epoch = FIXTURE_EPOCH
    e.cursor = String(cursorBase + i + 1)
    return e
  })
}
