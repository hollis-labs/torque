// Console event contract: a go-runtime-events-shaped envelope.
//
// The contract is deliberately tolerant and additive: `kind` is an open
// namespaced string, `payload` is a bag, unknown top-level fields survive in
// `extra`, and every optional field has a safe default after parsing. New kinds
// and fields from the data side must never require a GUI change to keep working;
// consumers narrow on `kind` and degrade to a generic row for kinds they do not know.
// Every string originates from agents or remote systems: render as text only.

export type ConsoleSeverity = 'debug' | 'info' | 'notice' | 'warning' | 'error' | 'critical'

export const SEVERITY_ORDER: readonly ConsoleSeverity[] = ['debug', 'info', 'notice', 'warning', 'error', 'critical']

/** Pointer to a thing an event is about (task, agent session, PR, thread...). `type` is open. */
export interface ConsoleRef {
  type: string
  id: string
  label?: string
  url?: string
}

export interface ConsoleSourceInfo {
  /** Producer id, e.g. "activity-monitor", "torque", "fixture". */
  id: string
  /** Optional machine/host discriminator for multi-machine feeds. */
  machine?: string
}

export type ConsoleActionKind = 'navigate' | 'api' | 'approve' | 'reject' | 'dismiss' | 'snooze' | 'reply' | (string & {})

/** An action the event offers. The GUI renders it and routes it through the ActionBus. */
export interface ConsoleActionOffer {
  id: string
  label: string
  kind: ConsoleActionKind
  /** Offered actions that authorise a change must be gated by IdentityConfirm. */
  requiresConfirm?: boolean
  /** Permission scopes the action needs, passed to IdentityConfirm. */
  scope?: string[]
  params?: Record<string, unknown>
}

export interface ConsoleEvent<P = Record<string, unknown>> {
  /** Stable unique id; the dedupe key. A repeated id replaces the earlier copy. */
  id: string
  /** Epoch ms. */
  ts: number
  /** Open namespaced kind, e.g. "approval.requested". See KINDS for the ones the GUI knows. */
  kind: string
  source: ConsoleSourceInfo
  subjects: ConsoleRef[]
  severity: ConsoleSeverity
  payload: P
  correlationId?: string
  threadId?: string
  actions: ConsoleActionOffer[]
  /** Unrecognised top-level fields, preserved verbatim for forward compatibility. */
  extra: Record<string, unknown>

  // --- Envelope v1.1 (DRAFT, all optional; see CONTRACT.md). Field names are proposals. ---
  /** Optional: daemon, fleet, message and task events have no session. */
  sessionId?: string
  /** Primary subject, when the producer sent a single `subject` {type,id}. Also present as subjects[0]. */
  subject?: ConsoleRef
  /** Dimension block stamped by the platform (project_id, task_id, worker_id...). Low-cardinality ids only. */
  scope?: ConsoleScope
  corr?: ConsoleCorr
  actor?: ConsoleActor
  class?: ConsoleClass
  /** Log-assigned, never producer-assigned. Epoch ms. */
  recordedAt?: number
  /** Changes when a producer or log restarts its sequence; cursors are only comparable within one epoch. */
  epoch?: string
  /** Log-assigned resume position for this event. Opaque. */
  cursor?: ConsoleCursor
}

export type ConsoleScope = Record<string, string | number | undefined>

export interface ConsoleCorr {
  traceId?: string
  spanId?: string
  causationId?: string
  correlationId?: string
  threadId?: string
  idempotencyKey?: string
  /** Hops through reactions (loop guard). */
  depth?: number
}

export type ActorKind = 'operator' | 'agent' | 'service' | 'system' | 'workflow' | 'hook' | 'reaction' | (string & {})

export interface ConsoleActor {
  kind: ActorKind
  id?: string
  onBehalfOf?: string
  verified?: 'token' | 'claimed' | 'none' | (string & {})
}

export type ConsoleCategory =
  | 'activity' | 'state' | 'progress' | 'data' | 'message' | 'tool' | 'approval' | 'audit' | 'alert' | 'fleet' | 'step'
  | (string & {})
export type ConsoleSensitivity = 'public' | 'private' | 'secret' | (string & {})
export type ConsoleVisibility = 'local' | 'environment' | 'mesh' | 'operator' | (string & {})

export interface ConsoleClass {
  category?: ConsoleCategory
  severity?: ConsoleSeverity
  sensitivity?: ConsoleSensitivity
  /** Sensitivity marker for detected PII. */
  pii?: boolean
  visibility?: ConsoleVisibility
  /** `id@version` */
  payloadSchema?: string
  labels?: string[]
  sanitized?: boolean
  redactions?: number
}

/** Kinds the GUI has typed payload knowledge about. Not exhaustive; kind stays an open string. */
export const KINDS = {
  agentState: 'agent.state',
  taskStateChanged: 'task.state_changed',
  messageReceived: 'message.received',
  approvalRequested: 'approval.requested',
  approvalResolved: 'approval.resolved',
  prOpened: 'pr.opened',
  prReviewRequested: 'pr.review_requested',
  prChecksFailed: 'pr.checks_failed',
  prMerged: 'pr.merged',
  metricTick: 'metric.tick',
  // --- DRAFT event/message stream families (event_message_stream_contract_2026_10_16) ---
  /** Prefix family: `<entity>.state_changed` for task, run, session, checkpoint, sprint, phase, claim, delivery. */
  runStateChanged: 'run.state_changed',
  sessionStateChanged: 'session.state_changed',
  gateEvaluated: 'gate.evaluated',
  phaseStarted: 'phase.started',
  phaseCompleted: 'phase.completed',
  progressCheckpoint: 'progress.checkpoint',
  dataEmitted: 'data.emitted',
  stepExecuted: 'step.executed',
  toolCallStarted: 'tool.call.started',
  toolCallFinished: 'tool.call.finished',
  fleetNodeDown: 'fleet.node.down',
  fleetNodeUp: 'fleet.node.up',
  fleetNodeRestarted: 'fleet.node.restarted',
  fleetNodeDegraded: 'fleet.node.degraded',
  eventClassified: 'event.classified',
  alertRaised: 'alert.raised',
  alertCleared: 'alert.cleared',
} as const

/** `message.*` lifecycle kinds (message.sent ... wake.outcome). The mailbox stays the system of record. */
export const MESSAGE_KINDS = [
  'message.sent', 'message.queued', 'message.delivered', 'message.read', 'message.consumed', 'message.failed',
  'message.dead_lettered', 'message.redriven', 'message.canceled', 'message.archived', 'message.purged',
  'message.wake.attempted', 'message.wake.outcome',
] as const

export function isStateChanged(kind: string): boolean {
  return kind.endsWith('.state_changed')
}
export function isMessageLifecycle(kind: string): boolean {
  return kind.startsWith('message.') && kind !== KINDS.messageReceived
}

export type KnownKind = (typeof KINDS)[keyof typeof KINDS]

export interface AgentStatePayload {
  state: 'waiting' | 'busy' | 'idle' | 'unknown'
  waitingFor?: string
  cwd?: string
  provider?: string
}
export interface TaskStateChangedPayload {
  from?: string
  to: string
  title?: string
}
export interface MessageReceivedPayload {
  from: string
  to?: string
  subject?: string
  preview?: string
}
export interface ApprovalRequestedPayload {
  title: string
  summary?: string
  requestedBy?: string
  expiresAt?: number
}
export interface PrPayload {
  repo: string
  number: number
  title?: string
  author?: string
  checks?: 'passing' | 'failing' | 'pending'
}
// Payload fields are snake_case, exactly as the producers send them (payload is an untouched bag).
export interface StateChangedPayload {
  entity?: { type: string; id: string }
  from?: string
  to: string
  trigger?: { kind: string; ref?: string }
  reason?: string
  guard?: { name: string; result: string }
  forced?: boolean
}
export interface GateEvaluatedPayload {
  gate: { id: string; kind: string }
  subject?: { type: string; id: string }
  result: 'pass' | 'fail' | 'blocked' | 'skipped'
  blocking?: boolean
  evidence_ref?: string
}
export interface PhasePayload {
  plan_id?: string
  phase_id: string
  name?: string
  order?: number
  progress?: { done: number; total: number }
}
export interface ProgressCheckpointPayload {
  detail_level?: 'off' | 'milestone' | 'step' | 'verbose' | (string & {})
  label?: string
  pct?: number
  step?: number
  total_steps?: number
  /** Max 600 chars. */
  summary?: string
  data_ref?: string
}
export interface DataEmittedPayload {
  schema: string
  data?: unknown
  ref?: string
}
export interface StepExecutedPayload {
  step_id: string
  rule_id?: string
  trigger_event_id?: string
  input_digest?: string
  outcome: 'ok' | 'skipped' | 'failed'
  effects?: unknown[]
}
export interface ToolCallPayload {
  tool: string
  server?: string
  args_bytes?: number
  ok?: boolean
  error_class?: string
  duration_ms?: number
  result_bytes?: number
  result_summary?: string
}
/** Message lifecycle: metadata and a body POINTER only. The body is never on the stream. */
export interface MessageLifecyclePayload {
  message_id: string
  thread_id?: string
  in_reply_to?: string
  from?: string
  to?: string[]
  msg_kind?: string
  urgency?: string
  interrupt?: boolean
  delivery?: { state?: string; attempt?: number; binding_generation?: number }
  body_ref?: string
  body_bytes?: number
  content_type?: string
}
export interface ApprovalLifecyclePayload {
  request_id: string
  class?: string
  requester?: string
  subject?: { type: string; id: string }
  expires_at?: number
  hitl_ref?: string
  outcome?: 'approved' | 'denied' | 'expired' | 'withdrawn'
  resolver?: { kind: string; id: string }
}
export interface FleetNodePayload {
  environment_id?: string
  authority?: string
  worker_id?: string
  reason?: string
  last_seen?: number
}
export interface AlertPayload {
  rule_id: string
  source_event_ids?: string[]
  labels?: string[]
  coalesce_key?: string
  summary?: string
  confidence?: string
}
export interface MetricTickPayload {
  metric: string
  value: number
  unit?: string
}

/** Narrows an event to a kind's payload type, or null. Payload fields are still untrusted: check before use. */
export function asKind<P>(e: ConsoleEvent, kind: string): ConsoleEvent<P> | null {
  return e.kind === kind ? (e as unknown as ConsoleEvent<P>) : null
}

/** Cursor is opaque to the GUI: it only hands back what the source gave it. */
export type ConsoleCursor = string

export interface ConsoleSnapshot {
  events: ConsoleEvent[]
  cursor: ConsoleCursor | null
  /** Log epoch the cursor belongs to, when the source has one. */
  epoch?: string | null
}

/** Gap frame detail (DRAFT: reason, earliest_available, high_water). All optional: some feeds send an empty frame. */
export interface GapInfo {
  reason?: string
  earliestAvailable?: ConsoleCursor
  highWater?: ConsoleCursor
}
