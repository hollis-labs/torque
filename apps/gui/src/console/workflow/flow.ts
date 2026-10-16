// Workflow definitions: JSON that declares trigger -> card -> action -> next
// trigger, so automations can author orchestration flows without GUI code.
// Everything here is pure and serialisable (see WORKFLOW.md for the authoring guide).
import type { ConsoleEvent, ConsoleSeverity } from '../events/types'
import { SEVERITY_ORDER } from '../events/types'
import type { ActionIntent, CardInstance, CardSurface } from '../cards/types'
import { WORKFLOW_CARD_KIND } from './model'

export const FLOW_ACT_KIND = 'workflow.flow.act'
export const FLOW_VERSION = 1

export interface FlowEventMatch {
  kinds?: string[]
  kindPrefix?: string
  minSeverity?: ConsoleSeverity
  subjectType?: string
  /** Shallow equality on payload keys. */
  payloadEquals?: Record<string, string | number | boolean>
}

export interface FlowTrigger {
  /** Wait for a matching Console event. */
  event?: FlowEventMatch
  /** Present as soon as the previous step's action completes. */
  immediate?: boolean
}

export interface FlowActionDef {
  id: string
  label: string
  intent?: ActionIntent
  /** ActionBus action to run before advancing. String params may use {input}, {runId}, {payload.x}. Omit to only advance. */
  dispatch?: { kind: string; params?: Record<string, unknown> }
  /** Run IdentityConfirm (these scopes) before the dispatch. */
  confirmScope?: string[]
  /** Next step id, or "end". */
  next: string
}

export interface FlowStep {
  id: string
  trigger: FlowTrigger
  card: {
    title: string
    summary?: string
    severity?: ConsoleSeverity
    surfaces?: CardSurface[]
    /** Free-text field whose value is available to the actions as {input}. */
    input?: { label: string; placeholder?: string; required?: boolean }
  }
  /** Empty = terminal notice: the flow ends when it is shown. */
  actions: FlowActionDef[]
  /** Auto-advance after this long without a decision (via onTimeout, default "end"). */
  timeoutMs?: number
  onTimeout?: string
}

export interface FlowDefinition {
  id: string
  version: typeof FLOW_VERSION
  title: string
  description?: string
  /** Step id whose trigger starts a new instance. */
  start: string
  steps: FlowStep[]
}

export interface FlowInstance {
  id: string
  defId: string
  stepId: string
  status: 'waiting' | 'presenting' | 'ended'
  /** Run (or other subject) id taken from the starting event; available as {runId}. */
  runId?: string
  startedAt: number
  presentedAt?: number
  triggerPayload: Record<string, unknown>
  history: { stepId: string; at: number; via: string }[]
  outcome?: string
}

/* -------------------------------- matching -------------------------------- */

export function matchesFlowEvent(m: FlowEventMatch, e: ConsoleEvent): boolean {
  if (m.kinds && !m.kinds.includes(e.kind)) return false
  if (m.kindPrefix && !e.kind.startsWith(m.kindPrefix)) return false
  if (m.minSeverity && SEVERITY_ORDER.indexOf(e.severity) < SEVERITY_ORDER.indexOf(m.minSeverity)) return false
  if (m.subjectType && !e.subjects.some((s) => s.type === m.subjectType)) return false
  if (m.payloadEquals) for (const [k, v] of Object.entries(m.payloadEquals)) if (e.payload[k] !== v) return false
  return true
}

export const stepOf = (def: FlowDefinition, id: string): FlowStep | undefined => def.steps.find((s) => s.id === id)

/* -------------------------------- templates -------------------------------- */

interface TemplateCtx {
  input?: string
  runId?: string
  payload: Record<string, unknown>
}

export function renderFlowTemplate(s: string, ctx: TemplateCtx): string {
  return s.replace(/\{([\w.]+)\}/g, (whole, path: string) => {
    if (path === 'input') return ctx.input ?? ''
    if (path === 'runId') return ctx.runId ?? ''
    if (path.startsWith('payload.')) {
      const v = ctx.payload[path.slice(8)]
      return typeof v === 'string' || typeof v === 'number' ? String(v) : ''
    }
    return whole
  })
}

function renderDeep(v: unknown, ctx: TemplateCtx): unknown {
  if (typeof v === 'string') return renderFlowTemplate(v, ctx)
  if (Array.isArray(v)) return v.map((x) => renderDeep(x, ctx))
  if (typeof v === 'object' && v !== null) return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, renderDeep(x, ctx)]))
  return v
}

/* ------------------------------- transitions ------------------------------- */

const enter = (inst: FlowInstance, def: FlowDefinition, stepId: string, at: number, via: string, status: FlowInstance['status']): FlowInstance => {
  const step = stepOf(def, stepId)
  const terminal = status === 'presenting' && step !== undefined && step.actions.length === 0
  return {
    ...inst,
    stepId,
    status: terminal ? 'ended' : status,
    presentedAt: status === 'presenting' ? at : undefined,
    history: [...inst.history, { stepId, at, via }],
    outcome: terminal ? step.card.title : inst.outcome,
  }
}

/** Starts an instance if the event matches the start step's trigger. */
export function flowStart(def: FlowDefinition, e: ConsoleEvent, instId: string): FlowInstance | undefined {
  const start = stepOf(def, def.start)
  if (!start?.trigger.event || !matchesFlowEvent(start.trigger.event, e)) return undefined
  const base: FlowInstance = {
    id: instId,
    defId: def.id,
    stepId: def.start,
    status: 'waiting',
    runId: typeof e.payload.runId === 'string' ? e.payload.runId : (e.subjects.find((s) => s.type === 'run')?.id ?? e.correlationId),
    startedAt: e.ts,
    triggerPayload: e.payload,
    history: [],
  }
  return enter(base, def, def.start, e.ts, `event:${e.kind}`, 'presenting')
}

/** A waiting instance advances when its current step's trigger event arrives. */
export function flowOnEvent(def: FlowDefinition, inst: FlowInstance, e: ConsoleEvent): FlowInstance {
  if (inst.status !== 'waiting') return inst
  const step = stepOf(def, inst.stepId)
  if (!step?.trigger.event || !matchesFlowEvent(step.trigger.event, e)) return inst
  return enter({ ...inst, triggerPayload: { ...inst.triggerPayload, ...e.payload } }, def, inst.stepId, e.ts, `event:${e.kind}`, 'presenting')
}

export interface FlowActionOutcome {
  inst: FlowInstance
  /** What to run before the advance is committed; templates already rendered. */
  dispatch?: { kind: string; params: Record<string, unknown> }
  action?: FlowActionDef
}

/** Resolves an action against the presenting step: the dispatch to run and the instance after advancing. */
export function flowAct(def: FlowDefinition, inst: FlowInstance, actionId: string, input: string | undefined, now: number): FlowActionOutcome {
  const step = stepOf(def, inst.stepId)
  const action = inst.status === 'presenting' ? step?.actions.find((a) => a.id === actionId) : undefined
  if (!step || !action) throw new Error(`Action "${actionId}" is not available`)
  if (step.card.input?.required && !input?.trim()) throw new Error(`${step.card.input.label} is required`)
  const ctx: TemplateCtx = { input: input?.trim(), runId: inst.runId, payload: inst.triggerPayload }
  const dispatch = action.dispatch ? { kind: action.dispatch.kind, params: renderDeep(action.dispatch.params ?? {}, ctx) as Record<string, unknown> } : undefined
  return { inst: advance(def, inst, action.next, now, `action:${action.id}`), dispatch, action }
}

function advance(def: FlowDefinition, inst: FlowInstance, next: string, now: number, via: string): FlowInstance {
  if (next === 'end') return { ...inst, status: 'ended', presentedAt: undefined, outcome: via.replace(/^action:/, ''), history: [...inst.history, { stepId: 'end', at: now, via }] }
  const step = stepOf(def, next)
  if (!step) return { ...inst, status: 'ended', outcome: `invalid next "${next}"` }
  return enter(inst, def, next, now, via, step.trigger.immediate ? 'presenting' : 'waiting')
}

export function flowTimeout(def: FlowDefinition, inst: FlowInstance, now: number): FlowInstance {
  const step = stepOf(def, inst.stepId)
  if (inst.status !== 'presenting' || !step?.timeoutMs || inst.presentedAt === undefined || now - inst.presentedAt < step.timeoutMs) return inst
  return advance(def, inst, step.onTimeout ?? 'end', now, 'timeout')
}

/* ---------------------------------- cards ---------------------------------- */

/** The card for an instance's current step; undefined while it waits on its trigger. */
export function flowCard(def: FlowDefinition, inst: FlowInstance, now = Date.now()): CardInstance | undefined {
  const step = stepOf(def, inst.stepId)
  if (!step) return undefined
  const ended = inst.status === 'ended'
  if (inst.status === 'waiting') return undefined
  const ctx: TemplateCtx = { runId: inst.runId, payload: inst.triggerPayload }
  const live = !ended
  return {
    id: `wf-flow:${inst.id}`,
    kind: WORKFLOW_CARD_KIND.decision,
    ruleId: `flow:${def.id}`,
    title: renderFlowTemplate(step.card.title, ctx),
    summary: step.card.summary ? renderFlowTemplate(step.card.summary, ctx) : undefined,
    severity: step.card.severity ?? 'notice',
    phase: live ? 'live' : 'resolved',
    firstTs: inst.startedAt,
    ts: inst.history[inst.history.length - 1]?.at ?? inst.startedAt,
    count: 1,
    surfaces: step.card.surfaces ?? ['tray', 'modal', 'inline', 'toast'],
    subjects: inst.runId ? [{ type: 'run', id: inst.runId }] : [],
    event: {
      id: `flow-${inst.id}`,
      ts: now,
      kind: 'workflow.flow.step',
      source: { id: 'workflow-flow' },
      subjects: [],
      severity: step.card.severity ?? 'notice',
      payload: { flowId: def.id, stepId: step.id },
      actions: [],
      extra: {},
    },
    actions: live
      ? step.actions.map((a) => ({ id: a.id, label: a.label, intent: a.intent ?? 'neutral', kind: FLOW_ACT_KIND, params: { instanceId: inst.id, actionId: a.id }, origin: 'declared' as const }))
      : [],
    ...(ended ? { resolution: { label: inst.outcome ?? 'Done', ts: inst.history[inst.history.length - 1]?.at ?? inst.startedAt } } : {}),
    props: { flow: { defId: def.id, instanceId: inst.id, stepId: step.id, input: step.card.input, steps: inst.history.length } },
  }
}

/* -------------------------------- validation -------------------------------- */

export interface FlowValidation {
  ok: boolean
  errors: string[]
  warnings: string[]
  def?: FlowDefinition
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

/** Validates untrusted JSON. Errors block loading; warnings (unreachable steps, no terminal) do not. */
export function validateFlow(raw: unknown): FlowValidation {
  const errors: string[] = []
  const warnings: string[] = []
  if (!isObj(raw)) return { ok: false, errors: ['Definition must be an object'], warnings }
  if (typeof raw.id !== 'string' || !raw.id) errors.push('id is required')
  if (raw.version !== FLOW_VERSION) errors.push(`version must be ${FLOW_VERSION}`)
  if (typeof raw.title !== 'string' || !raw.title) errors.push('title is required')
  const steps = Array.isArray(raw.steps) ? raw.steps : []
  if (steps.length === 0) errors.push('steps must be a non-empty array')
  const ids = new Set<string>()
  for (const [i, s] of steps.entries()) {
    if (!isObj(s) || typeof s.id !== 'string' || !s.id) {
      errors.push(`steps[${i}].id is required`)
      continue
    }
    if (ids.has(s.id)) errors.push(`duplicate step id "${s.id}"`)
    ids.add(s.id)
  }
  if (typeof raw.start !== 'string' || !ids.has(raw.start)) errors.push('start must name a step')
  const reach = new Set<string>(typeof raw.start === 'string' ? [raw.start] : [])
  let terminal = false
  for (const s of steps) {
    if (!isObj(s) || typeof s.id !== 'string') continue
    const trig = s.trigger
    if (!isObj(trig) || (!isObj(trig.event) && trig.immediate !== true)) errors.push(`step "${s.id}": trigger needs event or immediate`)
    if (s.id === raw.start && isObj(trig) && !isObj(trig.event)) errors.push('the start step needs an event trigger')
    if (!isObj(s.card) || typeof s.card.title !== 'string') errors.push(`step "${s.id}": card.title is required`)
    const actions = Array.isArray(s.actions) ? s.actions : null
    if (!actions) errors.push(`step "${s.id}": actions must be an array`)
    else if (actions.length === 0) terminal = true
    const aids = new Set<string>()
    for (const a of actions ?? []) {
      if (!isObj(a) || typeof a.id !== 'string' || typeof a.label !== 'string') {
        errors.push(`step "${s.id}": every action needs id and label`)
        continue
      }
      if (aids.has(a.id)) errors.push(`step "${s.id}": duplicate action "${a.id}"`)
      aids.add(a.id)
      if (typeof a.next !== 'string') errors.push(`step "${s.id}" action "${a.id}": next is required`)
      else if (a.next === 'end') terminal = true
      else if (!ids.has(a.next)) errors.push(`step "${s.id}" action "${a.id}": unknown next "${a.next}"`)
      else reach.add(a.next)
    }
    if (typeof s.onTimeout === 'string' && s.onTimeout !== 'end') {
      if (!ids.has(s.onTimeout)) errors.push(`step "${s.id}": unknown onTimeout "${s.onTimeout}"`)
      else reach.add(s.onTimeout)
    }
    if (s.onTimeout !== undefined && typeof s.timeoutMs !== 'number') warnings.push(`step "${s.id}": onTimeout without timeoutMs never fires`)
  }
  for (const id of ids) if (!reach.has(id)) warnings.push(`step "${id}" is unreachable`)
  if (!terminal && steps.length) warnings.push('no step ends the flow')
  return errors.length ? { ok: false, errors, warnings } : { ok: true, errors, warnings, def: raw as unknown as FlowDefinition }
}

/** Sample: an agent blocks -> alert -> HITL decision -> steer -> resolution. Authored purely as data. */
export const SAMPLE_BLOCKED_FLOW: FlowDefinition = {
  id: 'agent-blocked',
  version: 1,
  title: 'Unblock a stuck agent',
  description: 'A run turns blocked: alert the operator, ask how to proceed, steer the agent, confirm the resolution.',
  start: 'alert',
  steps: [
    {
      id: 'alert',
      trigger: { event: { kinds: ['run.state_changed'], payloadEquals: { to: 'blocked' } } },
      card: { title: 'Agent blocked: {payload.reason}', summary: 'Run {runId} stopped making progress.', severity: 'warning', surfaces: ['toast', 'tray', 'inline'] },
      actions: [
        { id: 'triage', label: 'Triage', intent: 'primary', next: 'decide' },
        { id: 'ignore', label: 'Ignore', next: 'end' },
      ],
      timeoutMs: 1_800_000,
      onTimeout: 'escalate',
    },
    {
      id: 'decide',
      trigger: { immediate: true },
      card: { title: 'How should the agent proceed?', summary: 'Blocked reason: {payload.reason}', severity: 'notice' },
      actions: [
        { id: 'redirect', label: 'Give new instructions', intent: 'primary', next: 'steer' },
        { id: 'pause', label: 'Pause the run', dispatch: { kind: 'workflow.steer', params: { runId: '{runId}', command: { type: 'pause', reason: 'paused from triage' } } }, next: 'end' },
        { id: 'escalate', label: 'Escalate', intent: 'danger', next: 'escalate' },
      ],
    },
    {
      id: 'steer',
      trigger: { immediate: true },
      card: { title: 'Redirect the agent', summary: 'The instruction is sent to the agent and the run resumes.', input: { label: 'Instruction', placeholder: 'e.g. skip the flaky suite and continue', required: true } },
      actions: [
        {
          id: 'send',
          label: 'Redirect',
          intent: 'primary',
          confirmScope: ['workflow:steer'],
          dispatch: { kind: 'workflow.steer', params: { runId: '{runId}', command: { type: 'redirect', instruction: '{input}' } } },
          next: 'resolved',
        },
        { id: 'back', label: 'Back', next: 'decide' },
      ],
    },
    {
      id: 'escalate',
      trigger: { immediate: true },
      card: { title: 'Escalate to the owner', severity: 'warning' },
      actions: [
        {
          id: 'go',
          label: 'Escalate now',
          intent: 'danger',
          dispatch: { kind: 'workflow.steer', params: { runId: '{runId}', command: { type: 'escalate', to: 'owner', note: 'blocked, no operator response' } } },
          next: 'end',
        },
      ],
    },
    {
      id: 'resolved',
      trigger: { event: { kinds: ['run.state_changed'], payloadEquals: { to: 'running' } } },
      card: { title: 'Agent unblocked and running again', severity: 'info' },
      actions: [],
    },
  ],
}
