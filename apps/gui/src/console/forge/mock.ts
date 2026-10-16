import type { ForgeClient } from './client'
import {
  ForgeError,
  prKey,
  repoKey,
  type AddCommentInput,
  type AssignInput,
  type CheckRun,
  type Commit,
  type DiffHunk,
  type DiffLine,
  type FileDiff,
  type ForgeComment,
  type ForgeUser,
  type ListPrsQuery,
  type Page,
  type PendingPrSummary,
  type PrRef,
  type PullRequest,
  type Repo,
  type RepoRef,
  type Review,
  type ReviewInput,
} from './types'

export const MOCK_REPO: RepoRef = { host: 'github', owner: 'hollis-labs', name: 'torque' }
const GO_REPO: RepoRef = { host: 'github', owner: 'hollis-labs', name: 'go-runtime' }

const U = {
  chris: { login: 'chrispian', name: 'Chrispian' } as ForgeUser,
  dana: { login: 'dana-reviewer', name: 'Dana Reyes' } as ForgeUser,
  agentGui: { login: 'agent-gui-builder', name: 'GUI Builder Agent', isAgent: true } as ForgeUser,
  agentData: { login: 'agent-data', name: 'Data Agent', isAgent: true } as ForgeUser,
  agentQa: { login: 'agent-qa', name: 'QA Agent', isAgent: true } as ForgeUser,
}

/** Build a hunk from compact lines: ' ctx', '+add', '-del'. Computes numbering. */
function hunk(oldStart: number, newStart: number, header: string, raw: string[]): DiffHunk {
  let o = oldStart
  let n = newStart
  const lines: DiffLine[] = raw.map((r) => {
    const t = r.slice(1)
    if (r[0] === '+') return { kind: 'add', text: t, newLine: n++ }
    if (r[0] === '-') return { kind: 'del', text: t, oldLine: o++ }
    return { kind: 'context', text: t, oldLine: o++, newLine: n++ }
  })
  return {
    oldStart,
    newStart,
    oldLines: o - oldStart,
    newLines: n - newStart,
    header,
    lines,
  }
}

function file(path: string, status: FileDiff['status'], hunks: DiffHunk[], extra: Partial<FileDiff> = {}): FileDiff {
  let additions = 0
  let deletions = 0
  for (const h of hunks) {
    for (const l of h.lines) {
      if (l.kind === 'add') additions++
      else if (l.kind === 'del') deletions++
    }
  }
  return { path, status, additions, deletions, hunks, ...extra }
}

function check(id: string, name: string, conclusion: CheckRun['conclusion'] | null, status: CheckRun['status'] = 'completed'): CheckRun {
  return {
    id,
    name,
    status,
    ...(conclusion ? { conclusion } : {}),
    startedAt: '2026-10-16T03:10:00Z',
    ...(status === 'completed' ? { completedAt: '2026-10-16T03:14:30Z' } : {}),
    url: `https://github.com/hollis-labs/torque/actions/runs/${id}`,
  }
}

interface Fixture {
  pr: PullRequest
  diff: FileDiff[]
  commits: Commit[]
  comments: ForgeComment[]
}

function buildFixtures(): Fixture[] {
  const url = (r: RepoRef, n: number) => `https://github.com/${r.owner}/${r.name}/pull/${n}`

  // 1: GUI agent alert tray PR, checks green, one review requesting changes.
  const diff1: FileDiff[] = [
    file('apps/gui/src/console/alerts/alert-tray.tsx', 'added', [
      hunk(0, 1, '', [
        "+import { useAlerts } from './use-alerts'",
        '+',
        '+export function AlertTray() {',
        '+  const { alerts, dismiss } = useAlerts()',
        '+  if (alerts.length === 0) return null',
        '+  return (',
        '+    <ul role="list" className="divide-y">',
        '+      {alerts.map((a) => (',
        '+        <li key={a.id} onClick={() => dismiss(a.id)}>{a.title}</li>',
        '+      ))}',
        '+    </ul>',
        '+  )',
        '+}',
      ]),
    ]),
    file('apps/gui/src/App.tsx', 'modified', [
      hunk(12, 12, 'function App()', [
        ' import { PlannerPage } from "./pages/PlannerPage"',
        '+import { AlertTray } from "./console/alerts/alert-tray"',
        ' ',
        ' export default function App() {',
        '   return (',
        '     <Shell>',
        '+      <AlertTray />',
        '       <Routes />',
        '     </Shell>',
      ]),
    ]),
  ]
  const pr1: PullRequest = {
    ref: { repo: MOCK_REPO, number: 271 },
    title: 'feat(console): alert tray with server-backed dismiss/snooze',
    body: 'Adds the alert tray (A1) behind the StateStore seam.\n\n- Tray mounts once at the shell\n- Dismiss and snooze go through StateStore\n\nTask: CW-20261016-0106',
    state: 'open',
    draft: false,
    author: U.agentGui,
    assignees: [U.dana],
    reviewers: [U.dana, U.chris],
    labels: ['console', 'gui'],
    baseBranch: 'feat/gui-agents-builder-spike',
    headBranch: 'console/a1',
    headSha: '9f3c2a1b7d0e4c55a1e2f3b4c5d6e7f801234567',
    mergeable: 'mergeable',
    checks: [check('9001', 'build', 'success'), check('9002', 'vitest', 'success'), check('9003', 'lint', 'success')],
    reviews: [
      { id: 'rv-1', author: U.dana, state: 'changes_requested', body: 'Dismiss on row click is surprising; use an explicit button.', submittedAt: '2026-10-16T05:02:00Z', commitSha: '9f3c2a1b7d0e4c55a1e2f3b4c5d6e7f801234567' },
    ],
    additions: 15,
    deletions: 0,
    changedFiles: 2,
    commentCount: 3,
    createdAt: '2026-10-16T04:40:00Z',
    updatedAt: '2026-10-16T05:10:00Z',
    url: url(MOCK_REPO, 271),
    taskId: 'CW-20261016-0106',
    agentId: 'agent-gui-builder',
  }
  const commits1: Commit[] = [
    { sha: '1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d', message: 'feat(console): add AlertTray skeleton', author: U.agentGui, authoredAt: '2026-10-16T04:41:00Z', parents: ['aa11bb22cc33dd44ee55ff66778899aabbccddee'] },
    { sha: '9f3c2a1b7d0e4c55a1e2f3b4c5d6e7f801234567', message: 'feat(console): mount tray at shell', author: U.agentGui, authoredAt: '2026-10-16T04:55:00Z', parents: ['1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d'] },
  ]
  const comments1: ForgeComment[] = [
    { kind: 'review', id: 'rc-11', author: U.dana, body: 'Row click dismissing is easy to hit by accident. Can this be a dedicated button?', createdAt: '2026-10-16T05:01:00Z', path: 'apps/gui/src/console/alerts/alert-tray.tsx', line: 9, side: 'right', commitSha: '9f3c2a1b7d0e4c55a1e2f3b4c5d6e7f801234567', resolved: false, reviewId: 'rv-1' },
    { kind: 'review', id: 'rc-12', author: U.agentGui, body: 'Good call, moving to an explicit dismiss button in the next push.', createdAt: '2026-10-16T05:08:00Z', path: 'apps/gui/src/console/alerts/alert-tray.tsx', line: 9, side: 'right', inReplyTo: 'rc-11', resolved: false },
    { kind: 'issue', id: 'ic-13', author: U.chris, body: 'Please keep the tray out of per-route streams.', createdAt: '2026-10-16T05:10:00Z' },
  ]

  // 2: data agent event contract, failing check + conflicting.
  const diff2: FileDiff[] = [
    file('apps/gui/src/console/events/types.ts', 'modified', [
      hunk(20, 20, 'export interface ConsoleEvent', [
        ' export interface ConsoleEvent {',
        '   id: string',
        '-  kind: string',
        '+  kind: ConsoleEventKind',
        '   at: string',
        '+  machine?: string',
        ' }',
      ]),
    ]),
    file('apps/gui/src/console/events/old-adapter.ts', 'removed', [
      hunk(1, 0, '', ['-export const legacy = true', '-export function adapt() {', '-  return []', '-}']),
    ]),
    file('apps/gui/src/console/events/adapter.ts', 'renamed', [
      hunk(1, 1, '', [' export function adapt(raw: unknown) {', '-  return []', '+  return normalize(raw)', ' }']),
    ], { oldPath: 'apps/gui/src/console/events/adapter.old.ts' }),
    file('apps/gui/public/fixtures/feed.png', 'added', [], { binary: true }),
  ]
  const pr2: PullRequest = {
    ref: { repo: MOCK_REPO, number: 268 },
    title: 'feat(console): F1 event contract, adapters and fixture feed',
    body: 'Defines ConsoleEvent and the FixtureSource.',
    state: 'open',
    draft: true,
    author: U.agentData,
    assignees: [],
    reviewers: [U.chris],
    labels: ['console', 'contract'],
    baseBranch: 'feat/gui-agents-builder-spike',
    headBranch: 'console/f1',
    headSha: 'c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00',
    mergeable: 'conflicting',
    checks: [check('9101', 'build', 'failure'), check('9102', 'vitest', null, 'in_progress'), check('9103', 'lint', 'skipped')],
    reviews: [],
    additions: 8,
    deletions: 7,
    changedFiles: 4,
    commentCount: 0,
    createdAt: '2026-10-15T22:00:00Z',
    updatedAt: '2026-10-16T03:20:00Z',
    url: url(MOCK_REPO, 268),
    taskId: 'CW-20261016-0100',
    agentId: 'agent-data',
  }
  const commits2: Commit[] = [
    { sha: 'c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00', message: 'wip: event kinds', author: U.agentData, authoredAt: '2026-10-16T03:19:00Z' },
  ]

  // 3: go-runtime, approved, ready to merge.
  const diff3: FileDiff[] = [
    file('internal/events/bus.go', 'modified', [
      hunk(41, 41, 'func (b *Bus) Publish', [
        ' func (b *Bus) Publish(ev Event) error {',
        '+\tif ev.Machine == "" {',
        '+\t\tev.Machine = b.machine',
        '+\t}',
        ' \treturn b.sink.Write(ev)',
        ' }',
      ]),
    ]),
  ]
  const pr3: PullRequest = {
    ref: { repo: GO_REPO, number: 54 },
    title: 'fix(events): stamp machine on published events',
    body: 'Multi-machine feed needs the origin machine on every event.',
    state: 'open',
    author: U.chris,
    assignees: [U.agentQa],
    reviewers: [U.dana],
    labels: ['bugfix'],
    baseBranch: 'main',
    headBranch: 'fix/event-machine',
    headSha: 'deadbeefdeadbeefdeadbeefdeadbeefdeadbeef',
    mergeable: 'mergeable',
    checks: [check('9201', 'go test', 'success'), check('9202', 'golangci-lint', 'success')],
    reviews: [{ id: 'rv-3', author: U.dana, state: 'approved', submittedAt: '2026-10-16T02:00:00Z' }],
    additions: 3,
    deletions: 0,
    changedFiles: 1,
    commentCount: 1,
    createdAt: '2026-10-15T20:00:00Z',
    updatedAt: '2026-10-16T02:00:00Z',
    url: url(GO_REPO, 54),
  }
  const commits3: Commit[] = [
    { sha: 'deadbeefdeadbeefdeadbeefdeadbeefdeadbeef', message: 'fix(events): stamp machine on publish', author: U.chris, authoredAt: '2026-10-15T20:01:00Z' },
  ]

  // 4: merged, historical.
  const pr4: PullRequest = {
    ref: { repo: MOCK_REPO, number: 266 },
    title: 'feat(gui): agents tab live feed',
    state: 'merged',
    author: U.agentGui,
    assignees: [],
    baseBranch: 'main',
    headBranch: 'gui/agents-live',
    mergeable: 'unknown',
    checks: [check('9301', 'build', 'success')],
    reviews: [{ id: 'rv-4', author: U.chris, state: 'approved', submittedAt: '2026-10-15T18:00:00Z' }],
    additions: 120,
    deletions: 30,
    changedFiles: 6,
    commentCount: 0,
    createdAt: '2026-10-15T15:00:00Z',
    updatedAt: '2026-10-15T18:30:00Z',
    mergedAt: '2026-10-15T18:30:00Z',
    url: url(MOCK_REPO, 266),
  }

  return [
    { pr: pr1, diff: diff1, commits: commits1, comments: comments1 },
    { pr: pr2, diff: diff2, commits: commits2, comments: [] },
    { pr: pr3, diff: diff3, commits: commits3, comments: [{ kind: 'issue', id: 'ic-31', author: U.dana, body: 'LGTM once CI is green.', createdAt: '2026-10-16T01:59:00Z' }] },
    { pr: pr4, diff: [], commits: [], comments: [] },
  ]
}

const REPOS: Repo[] = [
  { ...MOCK_REPO, description: 'Torque task orchestration', defaultBranch: 'main', private: true, stars: 12, openIssues: 7, updatedAt: '2026-10-16T05:10:00Z', url: 'https://github.com/hollis-labs/torque' },
  { ...GO_REPO, description: 'Go runtime and event bus', defaultBranch: 'main', private: true, stars: 3, openIssues: 2, updatedAt: '2026-10-16T02:00:00Z', url: 'https://github.com/hollis-labs/go-runtime' },
]

export interface MockForgeOptions {
  /** Simulated latency in ms (default 0). */
  latencyMs?: number
  /** Login that writes are attributed to. */
  actor?: ForgeUser
  now?: () => string
}

const clone = <T,>(v: T): T => structuredClone(v)

export class MockForgeClient implements ForgeClient {
  private fx = new Map<string, Fixture>()
  private seq = 100
  private latencyMs: number
  private actor: ForgeUser
  private now: () => string

  constructor(opts: MockForgeOptions = {}) {
    this.latencyMs = opts.latencyMs ?? 0
    this.actor = opts.actor ?? U.chris
    this.now = opts.now ?? (() => new Date().toISOString())
    for (const f of buildFixtures()) this.fx.set(prKey(f.pr.ref), f)
  }

  private async tick(): Promise<void> {
    if (this.latencyMs > 0) await new Promise((r) => setTimeout(r, this.latencyMs))
  }

  private get(ref: PrRef): Fixture {
    const f = this.fx.get(prKey(ref))
    if (!f) throw new ForgeError('not_found', `pull request not found: ${prKey(ref)}`)
    return f
  }

  async getRepo(repo: RepoRef): Promise<Repo> {
    await this.tick()
    const r = REPOS.find((x) => repoKey(x) === repoKey(repo))
    if (!r) throw new ForgeError('not_found', `repo not found: ${repoKey(repo)}`)
    return clone(r)
  }

  async listPrs(q: ListPrsQuery = {}): Promise<Page<PullRequest>> {
    await this.tick()
    const state = q.state ?? 'open'
    let items = [...this.fx.values()].map((f) => f.pr)
    if (q.repo) items = items.filter((p) => repoKey(p.ref.repo) === repoKey(q.repo!))
    if (state !== 'all') items = items.filter((p) => (state === 'open' ? p.state === 'open' : p.state === state))
    if (q.author) items = items.filter((p) => p.author.login === q.author)
    if (q.assignee) items = items.filter((p) => p.assignees.some((a) => a.login === q.assignee))
    if (q.query) {
      const s = q.query.toLowerCase()
      items = items.filter((p) => p.title.toLowerCase().includes(s) || (p.body ?? '').toLowerCase().includes(s))
    }
    items.sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
    const start = q.cursor ? Number(q.cursor) || 0 : 0
    const limit = q.limit && q.limit > 0 ? q.limit : items.length
    const slice = items.slice(start, start + limit)
    const next = start + limit < items.length ? String(start + limit) : null
    return { items: clone(slice), nextCursor: next }
  }

  async getPr(ref: PrRef): Promise<PullRequest> {
    await this.tick()
    return clone(this.get(ref).pr)
  }

  async getDiff(ref: PrRef): Promise<FileDiff[]> {
    await this.tick()
    return clone(this.get(ref).diff)
  }

  async listCommits(ref: PrRef): Promise<Commit[]> {
    await this.tick()
    return clone(this.get(ref).commits)
  }

  async listComments(ref: PrRef): Promise<ForgeComment[]> {
    await this.tick()
    return clone(this.get(ref).comments).sort((a, b) => a.createdAt.localeCompare(b.createdAt))
  }

  async addComment(ref: PrRef, input: AddCommentInput): Promise<ForgeComment> {
    await this.tick()
    const f = this.get(ref)
    if (!input.body.trim()) throw new ForgeError('invalid_args', 'comment body is required')
    const base = { id: `mc-${++this.seq}`, author: this.actor, body: input.body, createdAt: this.now() }
    let c: ForgeComment
    if (input.path) {
      if (!f.diff.some((d) => d.path === input.path)) throw new ForgeError('invalid_args', `path not in diff: ${input.path}`)
      c = { ...base, kind: 'review', path: input.path, line: input.line, side: input.side ?? 'right', inReplyTo: input.inReplyTo, resolved: false }
    } else {
      c = { ...base, kind: 'issue' }
    }
    f.comments.push(c)
    f.pr.commentCount = f.comments.length
    f.pr.updatedAt = base.createdAt
    return clone(c)
  }

  async reviewPr(ref: PrRef, input: ReviewInput): Promise<Review> {
    await this.tick()
    const f = this.get(ref)
    if (f.pr.state === 'merged' || f.pr.state === 'closed') throw new ForgeError('conflict', 'pull request is not open')
    if (input.verdict === 'request_changes' && !(input.body ?? '').trim() && !input.comments?.length) {
      throw new ForgeError('invalid_args', 'requesting changes needs a body or comments')
    }
    const state = input.verdict === 'approve' ? 'approved' : input.verdict === 'request_changes' ? 'changes_requested' : 'commented'
    const review: Review = { id: `rv-${++this.seq}`, author: this.actor, state, body: input.body, submittedAt: this.now(), commitSha: f.pr.headSha }
    f.pr.reviews.push(review)
    for (const c of input.comments ?? []) {
      await this.addComment(ref, c).then((made) => {
        if (made.kind === 'review') made.reviewId = review.id
        const stored = f.comments.find((x) => x.id === made.id)
        if (stored && stored.kind === 'review') stored.reviewId = review.id
      })
    }
    f.pr.updatedAt = review.submittedAt!
    return clone(review)
  }

  async assign(ref: PrRef, input: AssignInput): Promise<PullRequest> {
    await this.tick()
    const f = this.get(ref)
    const known = new Map<string, ForgeUser>(Object.values(U).map((u) => [u.login, u]))
    f.pr.assignees = input.assignees.map((login) => known.get(login) ?? { login })
    f.pr.updatedAt = this.now()
    return clone(f.pr)
  }

  async listPendingPrs(): Promise<PendingPrSummary[]> {
    await this.tick()
    const out: PendingPrSummary[] = []
    for (const { pr } of this.fx.values()) {
      if (pr.state !== 'open') continue
      const reasons: PendingPrSummary['reasons'] = []
      const failed = pr.checks.filter((c) => c.conclusion === 'failure' || c.conclusion === 'timed_out').length
      const pending = pr.checks.filter((c) => c.status !== 'completed').length
      const passed = pr.checks.filter((c) => c.conclusion === 'success').length
      const approved = pr.reviews.filter((r) => r.state === 'approved').length
      const changes = pr.reviews.filter((r) => r.state === 'changes_requested').length
      if (failed) reasons.push('checks_failed')
      if (changes) reasons.push('changes_requested')
      if (pr.mergeable === 'conflicting') reasons.push('conflicting')
      if (pr.assignees.length === 0) reasons.push('unassigned')
      if (!approved && !changes && pr.reviewers?.length) reasons.push('review_requested')
      if (!reasons.length) continue
      out.push({
        ref: clone(pr.ref),
        title: pr.title,
        author: clone(pr.author),
        assignees: clone(pr.assignees),
        state: pr.state,
        reasons,
        checksSummary: { passed, failed, pending },
        reviewSummary: { approved, changesRequested: changes },
        mergeable: pr.mergeable,
        updatedAt: pr.updatedAt,
        additions: pr.additions,
        deletions: pr.deletions,
        url: pr.url,
      })
    }
    return out.sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
  }
}

export function createMockForgeClient(opts?: MockForgeOptions): ForgeClient {
  return new MockForgeClient(opts)
}
