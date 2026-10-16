// Workflow <-> card pipeline glue. Event kinds are PLACEHOLDERS until the data
// team's stream design lands (draft: gate.evaluated, phase.*, <entity>.state_changed,
// step.executed, progress.checkpoint). They live only in WORKFLOW_EVENT_KINDS and
// the payload readers below, so remapping needs no component change.
import type { ConsoleEvent, ConsoleSeverity } from '../events/types'
import type { HitlAuditEntry } from '../hitl/types'
import type { CardActionSpec, CardInstance, CardSurface } from '../cards/types'
import type { GateDecision, PlanDecision, RunState, SteerCommand, WorkflowGate, WorkflowPhase, WorkflowRun } from './types'
import { ACTIVE_RUN_STATES } from './types'

export const WORKFLOW_EVENT_KINDS = {
  phaseStarted: 'phase.started',
  phaseCompleted: 'phase.completed',
  phaseFailed: 'phase.failed',
  gateEvaluated: 'gate.evaluated',
  stateChanged: 'run.state_changed',
  checkpoint: 'progress.checkpoint',
  step: 'step.executed',
} as const

export const WORKFLOW_CARD_KIND = {
  progress: 'workflow.progress',
  steer: 'workflow.steer',
  gate: 'workflow.gate',
  plan: 'workflow.plan',
  checkpoint: 'workflow.checkpoint',
  decision: 'workflow.decision',
} as const

/** ActionBus kinds registered by handlers.ts. */
export const WORKFLOW_ACTION = {
  steer: 'workflow.steer',
  gate: 'workflow.gate.submit',
  checkpoint: 'workflow.checkpoint.respond',
  plan: 'workflow.plan.decide',
} as const

export const nowAudit = (at: number, actor: string, action: string, note?: string): HitlAuditEntry => ({ at, actor, action, ...(note ? { note } : {}) })

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string | undefined => (typeof v === 'string' && v ? v : undefined)

/* ------------------------------ events -> run ------------------------------ */

export function runIdOf(e: ConsoleEvent): string | undefined {
  return str(e.payload.runId) ?? e.subjects.find((s) => s.type === 'run')?.id ?? e.corr?.correlationId ?? e.correlationId
}

const RUN_STATES: RunState[] = ['running', 'paused', 'blocked', 'awaiting_plan', 'completed', 'failed', 'cancelled']

/**
 * Pure fold of one event into the run it concerns. Returns the updated run, or
 * undefined when the event is not about a run (unknown kinds are ignored, never thrown).
 * An unknown run is created as a skeleton so a stream that starts mid-run still shows.
 */
export function applyRunEvent(runs: ReadonlyMap<string, WorkflowRun>, e: ConsoleEvent, now = e.ts): WorkflowRun | undefined {
  const K = WORKFLOW_EVENT_KINDS
  const known = Object.values(K) as string[]
  if (!known.includes(e.kind)) return undefined
  const id = runIdOf(e)
  if (!id) return undefined
  const base: WorkflowRun =
    runs.get(id) ?? { id, title: e.subjects.find((s) => s.type === 'run')?.label ?? str(e.payload.title) ?? id, state: 'running', priority: 'normal', agent: { id: e.actor?.id ?? e.source.id }, phases: [], subjects: e.subjects, audit: [], createdAt: e.ts, updatedAt: e.ts }
  const p = e.payload
  const phaseId = str(p.phaseId) ?? (isObj(p.phase) ? str(p.phase.id) : undefined)
  const phaseLabel = (isObj(p.phase) ? str(p.phase.label) : undefined) ?? str(p.label) ?? phaseId
  const upsertPhase = (patch: (ph: WorkflowPhase) => WorkflowPhase): WorkflowPhase[] => {
    if (!phaseId) return base.phases
    const has = base.phases.some((x) => x.id === phaseId)
    const list = has ? base.phases : [...base.phases, { id: phaseId, label: phaseLabel ?? phaseId, status: 'pending' as const }]
    return list.map((x) => (x.id === phaseId ? patch(x) : x))
  }
  let run: WorkflowRun = { ...base, updatedAt: Math.max(base.updatedAt, now) }
  switch (e.kind) {
    case K.phaseStarted:
      run = { ...run, phases: upsertPhase((x) => ({ ...x, status: 'active', startedAt: e.ts })) }
      break
    case K.phaseCompleted:
      run = { ...run, phases: upsertPhase((x) => ({ ...x, status: 'passed', endedAt: e.ts, progress: x.progress ? { ...x.progress, done: x.progress.total } : x.progress })) }
      break
    case K.phaseFailed:
      run = { ...run, phases: upsertPhase((x) => ({ ...x, status: 'failed', endedAt: e.ts })) }
      break
    case K.checkpoint: {
      const done = typeof p.done === 'number' ? p.done : undefined
      const total = typeof p.total === 'number' ? p.total : undefined
      if (done !== undefined && total !== undefined) run = { ...run, phases: upsertPhase((x) => ({ ...x, progress: { done, total } })) }
      break
    }
    case K.gateEvaluated: {
      const gid = str(p.gateId) ?? `${phaseId ?? 'phase'}-gate`
      const criteria = Array.isArray(p.criteria) ? p.criteria.filter(isObj).map((c) => ({ label: str(c.label) ?? '(criterion)', met: c.met === true })) : []
      const passed = p.passed === true
      const gate: WorkflowGate = { id: gid, label: str(p.label) ?? 'Gate', status: passed ? 'passed' : 'open', criteria, requiresHuman: p.requiresHuman !== false, evaluatedAt: e.ts }
      run = { ...run, phases: upsertPhase((x) => ({ ...x, gate })) }
      break
    }
    case K.stateChanged: {
      const to = RUN_STATES.find((s) => s === p.to)
      if (to) run = { ...run, state: to, blockedReason: to === 'blocked' ? (str(p.reason) ?? run.blockedReason) : undefined }
      break
    }
    default:
      break
  }
  return run
}

/* ------------------------------ run -> cards ------------------------------ */

const STATE_SEVERITY: Record<RunState, ConsoleSeverity> = { running: 'info', paused: 'notice', blocked: 'warning', awaiting_plan: 'notice', completed: 'info', failed: 'error', cancelled: 'info' }
export const STATE_LABEL: Record<RunState, string> = { running: 'Running', paused: 'Paused', blocked: 'Blocked', awaiting_plan: 'Awaiting plan', completed: 'Completed', failed: 'Failed', cancelled: 'Cancelled' }

const isActive = (r: WorkflowRun) => ACTIVE_RUN_STATES.includes(r.state)
const surfaces = (...s: CardSurface[]) => s

/** Phase/gate progress summary used by cards, chips and tests. */
export function runProgress(run: WorkflowRun): { passed: number; total: number; currentId?: string } {
  const passed = run.phases.filter((p) => p.status === 'passed' || p.status === 'skipped').length
  return { passed, total: run.phases.length, currentId: run.phases.find((p) => p.status === 'active')?.id }
}

function card(run: WorkflowRun, kind: string, key: string, title: string, over: Partial<CardInstance> & { severity: ConsoleSeverity; phase: CardInstance['phase']; surfaces: CardSurface[] }): CardInstance {
  return {
    id: `wf:${key}`,
    kind,
    ruleId: 'workflow',
    title,
    firstTs: run.createdAt,
    ts: run.updatedAt,
    count: 1,
    subjects: [{ type: 'run', id: run.id, label: run.title }, ...run.subjects.filter((s) => s.type !== 'run')],
    event: runToEvent(run),
    actions: [],
    props: { run },
    ...over,
  }
}

export function runToEvent(run: WorkflowRun): ConsoleEvent {
  return {
    id: `wf-${run.id}-${run.updatedAt}`,
    ts: run.updatedAt,
    kind: WORKFLOW_EVENT_KINDS.stateChanged,
    source: { id: 'workflow' },
    subjects: [{ type: 'run', id: run.id, label: run.title }],
    severity: STATE_SEVERITY[run.state],
    payload: { runId: run.id, to: run.state },
    correlationId: run.id,
    actions: [],
    extra: {},
  }
}

/** Every card a run presents: progress, steer (while active), one per human gate, plan, checkpoint. */
export function runToCards(run: WorkflowRun): CardInstance[] {
  const live = isActive(run)
  const out: CardInstance[] = [
    card(run, WORKFLOW_CARD_KIND.progress, `progress:${run.id}`, run.title, {
      summary: run.blockedReason ?? `${STATE_LABEL[run.state]}${run.agent.label ? ` - ${run.agent.label}` : ''}`,
      severity: STATE_SEVERITY[run.state],
      phase: live ? 'live' : 'resolved',
      surfaces: surfaces('tray', 'inline', 'modal'),
      ...(live ? {} : { resolution: { label: STATE_LABEL[run.state], ts: run.updatedAt } }),
    }),
  ]
  if (live) {
    out.push(card(run, WORKFLOW_CARD_KIND.steer, `steer:${run.id}`, `Steer: ${run.title}`, { severity: 'info', phase: 'live', surfaces: surfaces('tray', 'inline', 'modal') }))
  }
  for (const ph of run.phases) {
    const g = ph.gate
    if (!g || !g.requiresHuman) continue
    const open = g.status === 'open' && live
    out.push(
      card(run, WORKFLOW_CARD_KIND.gate, `gate:${run.id}:${g.id}`, `${g.label}: ${ph.label}`, {
        summary: g.criteria.length ? `${g.criteria.filter((c) => c.met).length}/${g.criteria.length} criteria met` : undefined,
        severity: open ? 'notice' : 'info',
        phase: open ? 'live' : 'resolved',
        surfaces: surfaces('tray', 'modal', 'inline', 'toast'),
        props: { run, gateId: g.id },
        ...(open ? {} : { resolution: { label: g.status === 'overridden' ? 'Overridden' : g.status === 'passed' ? 'Passed' : g.status === 'failed' ? 'Failed' : 'Closed', ts: run.updatedAt } }),
      }),
    )
  }
  if (run.plan) {
    const pending = run.plan.status === 'pending' && live
    out.push(
      card(run, WORKFLOW_CARD_KIND.plan, `plan:${run.id}`, `Plan: ${run.title}`, {
        summary: `${run.plan.items.length} proposed phases`,
        severity: pending ? 'notice' : 'info',
        phase: pending ? 'live' : 'resolved',
        surfaces: surfaces('tray', 'modal', 'inline', 'toast'),
        ...(pending ? {} : { resolution: { label: run.plan.status === 'approved' ? 'Approved' : 'Rejected', ts: run.updatedAt } }),
      }),
    )
  }
  const cp = run.checkpoint
  if (cp) {
    const pending = cp.status === 'pending' && live
    out.push(
      card(run, WORKFLOW_CARD_KIND.checkpoint, `cp:${run.id}:${cp.id}`, cp.question, {
        severity: pending ? 'warning' : 'info',
        phase: pending ? 'live' : 'resolved',
        surfaces: surfaces('tray', 'modal', 'inline', 'toast'),
        ts: cp.emittedAt,
        props: { run, checkpointId: cp.id },
        ...(pending ? {} : { resolution: { label: cp.status === 'answered' ? 'Answered' : 'Cancelled', ts: run.updatedAt } }),
      }),
    )
  }
  return out
}

/** Read the run back out of a card (props first). */
export function runFromCard(c: CardInstance): WorkflowRun | undefined {
  const r = c.props.run
  return isObj(r) && typeof r.id === 'string' && Array.isArray(r.phases) ? (r as unknown as WorkflowRun) : undefined
}

/* ------------------------------- action specs ------------------------------- */

const scopeConfirm = (action: string, title: string, run: Pick<WorkflowRun, 'id' | 'title'>, scope: string): NonNullable<CardActionSpec['confirm']> => ({
  what: { action, title, subject: { type: 'run', id: run.id, label: run.title } },
  scope: [scope],
})

/** Steering that changes what the agent does (redirect, priority) is step-up gated; pause/resume/escalate are the safe direction. */
export function steerAction(run: Pick<WorkflowRun, 'id' | 'title'>, cmd: SteerCommand): CardActionSpec {
  const gated = cmd.type === 'redirect' || cmd.type === 'priority'
  const label = { pause: 'Pause', resume: 'Resume', redirect: 'Redirect', escalate: 'Escalate', priority: 'Set priority' }[cmd.type]
  return {
    id: cmd.type,
    label,
    intent: cmd.type === 'pause' ? 'neutral' : cmd.type === 'escalate' ? 'danger' : 'primary',
    kind: WORKFLOW_ACTION.steer,
    params: { runId: run.id, command: cmd },
    origin: 'declared',
    ...(gated ? { confirm: scopeConfirm(`workflow.steer.${cmd.type}`, `${label}: ${run.title}`, run, 'workflow:steer') } : {}),
  }
}

export function gateAction(run: Pick<WorkflowRun, 'id' | 'title'>, gate: Pick<WorkflowGate, 'id' | 'label'>, decision: GateDecision, note?: string): CardActionSpec {
  const label = { pass: 'Pass gate', fail: 'Fail gate', override: 'Override' }[decision]
  return {
    id: decision,
    label,
    intent: decision === 'pass' ? 'primary' : decision === 'fail' ? 'danger' : 'neutral',
    kind: WORKFLOW_ACTION.gate,
    params: { runId: run.id, gateId: gate.id, decision, note },
    origin: 'declared',
    ...(decision !== 'fail' ? { confirm: scopeConfirm(`workflow.gate.${decision}`, `${label}: ${gate.label}`, run, 'workflow:gate') } : {}),
  }
}

export function planAction(run: Pick<WorkflowRun, 'id' | 'title'>, decision: PlanDecision, included: string[], note?: string): CardActionSpec {
  return {
    id: decision,
    label: decision === 'approve' ? 'Approve plan' : 'Reject plan',
    intent: decision === 'approve' ? 'primary' : 'danger',
    kind: WORKFLOW_ACTION.plan,
    params: { runId: run.id, decision, included, note },
    origin: 'declared',
    ...(decision === 'approve' ? { confirm: scopeConfirm('workflow.plan.approve', `Approve plan: ${run.title}`, run, 'workflow:plan:approve') } : {}),
  }
}

export function checkpointAction(run: Pick<WorkflowRun, 'id'>, checkpointId: string, answer: string): CardActionSpec {
  return { id: 'answer', label: 'Send answer', intent: 'primary', kind: WORKFLOW_ACTION.checkpoint, params: { runId: run.id, checkpointId, answer }, origin: 'declared' }
}
