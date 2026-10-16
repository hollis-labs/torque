import type { ConsoleSource, SnapshotOptions, SubscribeHandlers } from './source'
import { familyEvents, FIXTURE_EPOCH } from './fixture-families'
import { KINDS, type ConsoleCursor, type ConsoleEvent, type ConsoleSnapshot, type GapInfo } from './types'

export interface FixtureSourceOptions {
  seed?: number
  /** Stream cadence. 0 disables the timer: drive it with emit()/tick() (tests). */
  intervalMs?: number
  now?: () => number
  /** Events pre-loaded into the snapshot (default 14). */
  initial?: number
  /** Also load/emit one sample of every DRAFT kind family (default: only when `initial` is not given). */
  families?: boolean
}

const AGENTS = ['agent-alpha', 'agent-bravo', 'agent-charlie']
const REPOS = ['hollis-labs/torque', 'hollis-labs/plugins']
const TASKS = ['CW-20261016-0100', 'CW-20261016-0105', 'CW-20261016-0111']

/** Small deterministic PRNG so fixtures are reproducible per seed. */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

/**
 * Simulated feed of realistic events: state changes, messages, approvals, PR
 * events, metric ticks. Implements the same ConsoleSource seam as live feeds, so
 * the entire Console can be built and tested without a backend.
 */
export class FixtureSource implements ConsoleSource {
  readonly id = 'fixture'
  private readonly rnd: () => number
  private readonly now: () => number
  private readonly intervalMs: number
  private log: ConsoleEvent[] = []
  private seq = 0
  private handlers = new Set<SubscribeHandlers>()
  private timer: ReturnType<typeof setInterval> | null = null
  private readonly families: boolean
  private familyQueue: ConsoleEvent[] = []
  private ticks = 0
  private epoch: string | null = null

  constructor(opts: FixtureSourceOptions = {}) {
    this.rnd = mulberry32(opts.seed ?? 1)
    this.now = opts.now ?? Date.now
    this.intervalMs = opts.intervalMs ?? 4000
    const n = opts.initial ?? 14
    const t0 = this.now()
    for (let i = 0; i < n; i++) this.log.push(this.make(t0 - (n - i) * 60_000))
    this.families = opts.families ?? opts.initial === undefined
    if (this.families) {
      // Samples preload the snapshot, so every kind family is visible immediately.
      this.epoch = FIXTURE_EPOCH
      this.log.push(...familyEvents(t0 - 20 * 60_000))
    }
  }

  private pick<T>(xs: T[]): T {
    return xs[Math.floor(this.rnd() * xs.length)]
  }

  /** Builds the next fixture event (does not publish it). */
  private make(ts: number): ConsoleEvent {
    const n = ++this.seq
    const id = `fx-${n}`
    const source = { id: 'fixture', machine: this.pick(['hub', 'laptop']) }
    const agent = this.pick(AGENTS)
    const task = this.pick(TASKS)
    const repo = this.pick(REPOS)
    const prNum = 200 + Math.floor(this.rnd() * 80)
    const prRef = { type: 'pr', id: `${repo}#${prNum}`, label: `${repo}#${prNum}` }
    const agentRef = { type: 'agent', id: agent, label: agent }
    const taskRef = { type: 'task', id: task, label: task }
    const base = { id, ts, source, extra: {} as Record<string, unknown> }
    switch (n % 8) {
      case 1:
        return { ...base, kind: KINDS.agentState, severity: 'info', subjects: [agentRef], actions: [],
          payload: { state: this.pick(['busy', 'idle', 'waiting']), provider: 'claude', cwd: '/srv/work/torque' } }
      case 2:
        return { ...base, kind: KINDS.taskStateChanged, severity: 'info', subjects: [taskRef], actions: [], threadId: task,
          payload: { from: 'in_progress', to: this.pick(['review', 'done', 'blocked']), title: 'Console foundation' } }
      case 3:
        return { ...base, kind: KINDS.messageReceived, severity: 'notice', subjects: [agentRef], threadId: `thr-${agent}`,
          actions: [{ id: 'reply', label: 'Reply', kind: 'reply' }, { id: 'dismiss', label: 'Dismiss', kind: 'dismiss' }],
          payload: { from: agent, subject: 'Need a decision', preview: 'Should I proceed with the schema change?' } }
      case 4:
        return { ...base, kind: KINDS.approvalRequested, severity: 'warning', subjects: [agentRef, taskRef], correlationId: `appr-${n}`,
          actions: [
            { id: 'approve', label: 'Approve', kind: 'approve', requiresConfirm: true, scope: ['agent:apply'] },
            { id: 'reject', label: 'Reject', kind: 'reject' },
          ],
          payload: { title: 'Apply agent config change', summary: 'Adds the code-review tool to the agent.', requestedBy: agent, expiresAt: ts + 3_600_000 } }
      case 5:
        return { ...base, kind: KINDS.prOpened, severity: 'info', subjects: [prRef], threadId: prRef.id,
          actions: [{ id: 'open', label: 'Open PR', kind: 'navigate', params: { to: `/prs/${prNum}` } }],
          payload: { repo, number: prNum, title: 'Add console event contract', author: agent, checks: 'pending' } }
      case 6:
        return { ...base, kind: KINDS.prChecksFailed, severity: 'error', subjects: [prRef], threadId: prRef.id,
          actions: [{ id: 'open', label: 'Open PR', kind: 'navigate', params: { to: `/prs/${prNum}` } }],
          payload: { repo, number: prNum, title: 'Add console event contract', checks: 'failing' } }
      case 7:
        return { ...base, kind: KINDS.prReviewRequested, severity: 'notice', subjects: [prRef], threadId: prRef.id, actions: [],
          payload: { repo, number: prNum, author: agent } }
      default:
        return { ...base, kind: KINDS.metricTick, severity: 'debug', subjects: [], actions: [],
          payload: { metric: this.pick(['tokens_per_min', 'active_agents', 'queue_depth']), value: Math.round(this.rnd() * 100) } }
    }
  }

  private cursor(): ConsoleCursor {
    return String(this.log.length)
  }

  async snapshot(opts: SnapshotOptions = {}): Promise<ConsoleSnapshot> {
    const since = opts.since ? Number(opts.since) : 0
    const events = this.log.slice(Number.isFinite(since) ? since : 0)
    return { events, cursor: this.cursor(), epoch: this.epoch }
  }

  subscribe(handlers: SubscribeHandlers): () => void {
    this.handlers.add(handlers)
    queueMicrotask(() => {
      if (this.handlers.has(handlers)) handlers.onStatus('live')
    })
    if (this.intervalMs > 0 && !this.timer) this.timer = setInterval(() => this.tick(), this.intervalMs)
    return () => {
      this.handlers.delete(handlers)
      if (this.handlers.size === 0 && this.timer) {
        clearInterval(this.timer)
        this.timer = null
      }
    }
  }

  /** Generates and publishes the next simulated event. */
  tick(): ConsoleEvent {
    this.ticks++
    if (this.families && this.ticks % 5 === 0) {
      // Every 5th tick replays a family sample with a fresh id so the live stream shows new kinds too.
      if (this.familyQueue.length === 0) this.familyQueue = familyEvents(this.now())
      const s = this.familyQueue.shift()!
      const live = { ...s, id: `${s.id}-t${this.ticks}`, ts: this.now() }
      this.emit(live)
      return live
    }
    const e = this.make(this.now())
    this.emit(e)
    return e
  }

  /** Publish a specific event (tests, debug panel). It is also kept for later snapshots. */
  emit(e: ConsoleEvent): void {
    this.log.push(e)
    this.handlers.forEach((h) => h.onEvents([e], this.cursor(), e.epoch ?? this.epoch ?? undefined))
  }

  /** Simulates a dropped stream: subscribers are told to resync. */
  simulateGap(info?: GapInfo): void {
    this.handlers.forEach((h) => h.onGap(info))
  }

  /** Simulates a producer/log restart: new epoch, sequence restarts at 0, old cursors are invalid. */
  restartEpoch(): void {
    this.epoch = `${FIXTURE_EPOCH}-r${++this.ticks}`
    this.log = []
    this.handlers.forEach((h) => h.onGap({ reason: 'epoch_restart', earliestAvailable: '0', highWater: '0' }))
  }
}
