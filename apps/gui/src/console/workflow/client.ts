// WorkflowClient: the typed seam between workflow cards and the orchestration
// backend (Torque checkpoints, Hadron gates/signals, agent steering via M1).
// MockWorkflowClient is the only implementation; needs are in BACKEND_NEEDS.md.
import type { ConsoleEvent } from '../events/types'
import { isGranted, type ConfirmResult } from '../seams/identity-confirm'
import { applyRunEvent, nowAudit } from './model'
import type { GateDecision, PlanDecision, RunPriority, SteerCommand, WorkflowRun } from './types'
import { ACTIVE_RUN_STATES, PRIORITIES } from './types'

export interface WorkflowCtx {
  /** Present when the ActionBus ran IdentityConfirm. Forward `token` to the backend; never log it. */
  confirmation?: ConfirmResult
}

export interface WorkflowClient {
  list(): Promise<WorkflowRun[]>
  get(id: string): Promise<WorkflowRun | undefined>
  steer(runId: string, cmd: SteerCommand, ctx: WorkflowCtx): Promise<WorkflowRun>
  /** hadron_workflow_gate_submit. */
  submitGate(runId: string, gateId: string, decision: GateDecision, note: string | undefined, ctx: WorkflowCtx): Promise<WorkflowRun>
  /** torque_task_checkpoint_respond. */
  respondCheckpoint(runId: string, checkpointId: string, answer: string, ctx: WorkflowCtx): Promise<WorkflowRun>
  /** `included` is the edited subset of plan item ids; approving with fewer than proposed is an edit. */
  decidePlan(runId: string, decision: PlanDecision, included: string[], note: string | undefined, ctx: WorkflowCtx): Promise<WorkflowRun>
  subscribe(listener: (changed: WorkflowRun[]) => void): () => void
  /** Optional: a mock/local client folds Console events itself; a live client gets projections from the server. */
  ingest?(event: ConsoleEvent): void
}

export class WorkflowError extends Error {
  code: 'not_found' | 'not_active' | 'confirm_required' | 'invalid' | 'conflict'
  constructor(code: WorkflowError['code'], message: string) {
    super(message)
    this.code = code
  }
}

export interface MockWorkflowOptions {
  now?: () => number
  latencyMs?: number
}

export class MockWorkflowClient implements WorkflowClient {
  private runs = new Map<string, WorkflowRun>()
  private listeners = new Set<(c: WorkflowRun[]) => void>()
  private now: () => number
  private latencyMs: number
  /** Every accepted mutation, for tests and the preview. Contains no confirmation tokens. */
  readonly calls: { op: string; runId: string; detail?: unknown }[] = []

  constructor(initial: WorkflowRun[] = [], opts: MockWorkflowOptions = {}) {
    this.now = opts.now ?? Date.now
    this.latencyMs = opts.latencyMs ?? 0
    initial.forEach((r) => this.runs.set(r.id, r))
  }

  private wait = () => (this.latencyMs ? new Promise((r) => setTimeout(r, this.latencyMs)) : Promise.resolve())
  private put(run: WorkflowRun): WorkflowRun {
    this.runs.set(run.id, run)
    this.listeners.forEach((l) => l([run]))
    return run
  }
  private active(id: string): WorkflowRun {
    const r = this.runs.get(id)
    if (!r) throw new WorkflowError('not_found', `Run ${id} not found`)
    if (!ACTIVE_RUN_STATES.includes(r.state)) throw new WorkflowError('not_active', `Run is ${r.state}`)
    return r
  }
  private needConfirm(ctx: WorkflowCtx, what: string): ConfirmResult {
    if (!ctx.confirmation || !isGranted(ctx.confirmation, this.now())) throw new WorkflowError('confirm_required', `${what} requires identity confirmation`)
    return ctx.confirmation
  }
  private log(r: WorkflowRun, action: string, note?: string, c?: ConfirmResult): WorkflowRun {
    const who = c?.who.id ?? 'operator'
    const audit = [...r.audit]
    if (c) audit.push(nowAudit(this.now(), who, 'confirmed', c.scope.join(', ')))
    audit.push(nowAudit(this.now(), who, action, note))
    return { ...r, audit, updatedAt: this.now() }
  }

  async list() {
    await this.wait()
    return [...this.runs.values()]
  }
  async get(id: string) {
    await this.wait()
    return this.runs.get(id)
  }

  async steer(runId: string, cmd: SteerCommand, ctx: WorkflowCtx) {
    await this.wait()
    const r = this.active(runId)
    let c: ConfirmResult | undefined
    let next: WorkflowRun = r
    switch (cmd.type) {
      case 'pause':
        if (r.state === 'paused') throw new WorkflowError('conflict', 'Already paused')
        next = { ...r, state: 'paused' }
        break
      case 'resume':
        if (r.state !== 'paused' && r.state !== 'blocked') throw new WorkflowError('conflict', `Cannot resume a ${r.state} run`)
        next = { ...r, state: 'running', blockedReason: undefined }
        break
      case 'redirect':
        if (!cmd.instruction.trim()) throw new WorkflowError('invalid', 'Redirect needs an instruction')
        c = this.needConfirm(ctx, 'Redirect')
        next = { ...r, redirect: cmd.instruction.trim(), state: r.state === 'blocked' ? 'running' : r.state, blockedReason: undefined }
        break
      case 'escalate':
        if (!cmd.to.trim()) throw new WorkflowError('invalid', 'Escalate needs a target')
        next = { ...r, escalatedTo: cmd.to.trim() }
        break
      case 'priority':
        if (!PRIORITIES.includes(cmd.priority)) throw new WorkflowError('invalid', 'Unknown priority')
        c = this.needConfirm(ctx, 'Changing priority')
        next = { ...r, priority: cmd.priority as RunPriority }
        break
    }
    this.calls.push({ op: `steer.${cmd.type}`, runId, detail: cmd })
    const note = cmd.type === 'redirect' ? cmd.instruction : cmd.type === 'escalate' ? `to ${cmd.to}` : cmd.type === 'priority' ? cmd.priority : undefined
    return this.put(this.log(next, `steer:${cmd.type}`, note, c))
  }

  async submitGate(runId: string, gateId: string, decision: GateDecision, note: string | undefined, ctx: WorkflowCtx) {
    await this.wait()
    const r = this.active(runId)
    const idx = r.phases.findIndex((p) => p.gate?.id === gateId)
    const gate = idx >= 0 ? r.phases[idx].gate : undefined
    if (!gate || gate.status !== 'open') throw new WorkflowError('conflict', 'Gate is not open')
    if (decision === 'override' && !note?.trim()) throw new WorkflowError('invalid', 'An override needs a reason')
    // Passing or overriding authorises the run to continue: step-up. Failing is the safe direction.
    const c = decision === 'fail' ? undefined : this.needConfirm(ctx, 'Passing a gate')
    const status = decision === 'pass' ? 'passed' : decision === 'override' ? 'overridden' : 'failed'
    const phases = r.phases.map((p, i) => {
      if (i === idx) return { ...p, gate: { ...gate, status } as typeof gate, status: decision === 'fail' ? ('failed' as const) : ('passed' as const), endedAt: this.now() }
      if (decision !== 'fail' && i === idx + 1 && p.status === 'pending') return { ...p, status: 'active' as const, startedAt: this.now() }
      return p
    })
    this.calls.push({ op: 'gate.submit', runId, detail: { gateId, decision } })
    return this.put(this.log({ ...r, phases, state: decision === 'fail' ? 'blocked' : r.state, blockedReason: decision === 'fail' ? `Gate "${gate.label}" failed` : r.blockedReason }, `gate:${decision}`, note, c))
  }

  async respondCheckpoint(runId: string, checkpointId: string, answer: string, ctx: WorkflowCtx) {
    void ctx
    await this.wait()
    const r = this.active(runId)
    const cp = r.checkpoint
    if (!cp || cp.id !== checkpointId || cp.status !== 'pending') throw new WorkflowError('conflict', 'Checkpoint is not pending')
    if (!answer.trim()) throw new WorkflowError('invalid', 'Answer is empty')
    if (!cp.allowText && cp.options.length && !cp.options.some((o) => o.value === answer)) throw new WorkflowError('invalid', 'Not one of the options')
    this.calls.push({ op: 'checkpoint.respond', runId, detail: { checkpointId } })
    return this.put(this.log({ ...r, checkpoint: { ...cp, status: 'answered', answer }, state: r.state === 'blocked' ? 'running' : r.state, blockedReason: undefined }, 'checkpoint:answered', answer))
  }

  async decidePlan(runId: string, decision: PlanDecision, included: string[], note: string | undefined, ctx: WorkflowCtx) {
    await this.wait()
    const r = this.active(runId)
    const plan = r.plan
    if (!plan || plan.status !== 'pending') throw new WorkflowError('conflict', 'No plan is pending')
    if (decision === 'approve' && included.length === 0) throw new WorkflowError('invalid', 'Include at least one phase or reject the plan')
    const c = decision === 'approve' ? this.needConfirm(ctx, 'Approving a plan') : undefined
    const items = plan.items.map((i) => ({ ...i, included: included.includes(i.id) }))
    const edited = decision === 'approve' && items.some((i) => !i.included)
    this.calls.push({ op: 'plan.decide', runId, detail: { decision, included } })
    return this.put(
      this.log(
        { ...r, plan: { ...plan, items, status: decision === 'approve' ? 'approved' : 'rejected', note }, state: decision === 'approve' ? 'running' : 'blocked', blockedReason: decision === 'reject' ? 'Plan rejected' : undefined },
        edited ? 'plan:approved-edited' : `plan:${decision === 'approve' ? 'approved' : 'rejected'}`,
        note,
        c,
      ),
    )
  }

  subscribe(l: (c: WorkflowRun[]) => void) {
    this.listeners.add(l)
    return () => this.listeners.delete(l)
  }

  ingest(event: ConsoleEvent) {
    const cur = applyRunEvent(this.runs, event, this.now())
    if (cur) this.put(cur)
  }
}
