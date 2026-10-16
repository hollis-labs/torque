// Detect-and-surface: turn the pending-PR list into Console events so K1 cards
// and A1 alerts appear without any inbox-specific UI. The kind mapping is a
// swappable function; the source polls the ForgeClient (until the backend emits
// pr.* events itself, see ../BACKEND_NEEDS.md) and can be composed with other
// feeds via CompositeSource.
import { defaultAlertMapper, linkOf, sourceOf } from '../../alerts/mapping'
import type { Alert, AlertMapper } from '../../alerts/types'
import type { ConsoleSource, SubscribeHandlers, SnapshotOptions } from '../../events/source'
import { KINDS, type ConsoleEvent, type ConsoleSnapshot } from '../../events/types'
import type { ForgeClient } from '../client'
import { prKey, type PendingPrSummary } from '../types'
import { categoriesOf, DEFAULT_STALE_MS, type InboxCategory } from './model'
import { prWorkspaceHref, PR_INBOX_PATH } from './href'

export const PR_KINDS = {
  changesRequested: 'pr.changes_requested',
  conflicting: 'pr.conflicting',
  stale: 'pr.stale',
} as const

export interface PendingPrEventContext {
  now: number
  staleMs: number
}

/** Maps one pending PR to zero or more events. Replace wholesale to change what surfaces. */
export type PendingPrEventMapper = (pr: PendingPrSummary, ctx: PendingPrEventContext) => ConsoleEvent[]

const KIND_FOR: Partial<Record<InboxCategory, { kind: string; severity: ConsoleEvent['severity'] }>> = {
  failing_checks: { kind: KINDS.prChecksFailed, severity: 'error' },
  changes_requested: { kind: PR_KINDS.changesRequested, severity: 'warning' },
  conflicts: { kind: PR_KINDS.conflicting, severity: 'warning' },
  needs_review: { kind: KINDS.prReviewRequested, severity: 'notice' },
  stale: { kind: PR_KINDS.stale, severity: 'info' },
}

/**
 * One event per PR, for its most urgent category (K1 coalesces on `pr:{subject.pr}`
 * anyway). PRs that are only "unassigned" stay in the inbox without an alert.
 * The event id embeds the PR's updatedAt, so an unchanged PR never re-alerts.
 */
export const defaultPendingPrEventMapper: PendingPrEventMapper = (pr, ctx) => {
  const primary = categoriesOf(pr, ctx.now, ctx.staleMs)[0]
  const def = primary && KIND_FOR[primary]
  if (!def) return []
  const id = prKey(pr.ref)
  const label = `${pr.ref.repo.owner}/${pr.ref.repo.name}#${pr.ref.number}`
  const href = prWorkspaceHref(pr.ref)
  const checks = pr.checksSummary
  return [
    {
      id: `forge-inbox:${id}:${def.kind}:${pr.updatedAt}`,
      ts: Date.parse(pr.updatedAt) || ctx.now,
      kind: def.kind,
      source: { id: 'forge-inbox' },
      subjects: [{ type: 'pr', id, label, url: pr.url }],
      severity: def.severity,
      threadId: id,
      payload: {
        repo: `${pr.ref.repo.owner}/${pr.ref.repo.name}`,
        number: pr.ref.number,
        title: pr.title,
        author: pr.author.login,
        reasons: pr.reasons,
        checks: checks ? (checks.failed ? 'failing' : checks.pending ? 'pending' : 'passing') : undefined,
      },
      actions: [
        { id: 'open', label: 'Open PR', kind: 'navigate', params: { href, to: href } },
        { id: 'triage', label: 'Triage', kind: 'navigate', params: { href: PR_INBOX_PATH, to: PR_INBOX_PATH } },
      ],
      extra: {},
    },
  ]
}

export interface PendingPrSourceOptions {
  mapper?: PendingPrEventMapper
  /** Poll interval; 0 disables polling (snapshots only). Default 60s. */
  pollMs?: number
  staleMs?: number
  now?: () => number
}

/** ConsoleSource over ForgeClient.listPendingPrs(). */
export class PendingPrSource implements ConsoleSource {
  readonly id = 'forge-inbox'
  private readonly mapper: PendingPrEventMapper
  private readonly pollMs: number
  private readonly staleMs: number
  private readonly now: () => number
  private readonly client: ForgeClient
  private seen = new Set<string>()

  constructor(client: ForgeClient, opts: PendingPrSourceOptions = {}) {
    this.client = client
    this.mapper = opts.mapper ?? defaultPendingPrEventMapper
    this.pollMs = opts.pollMs ?? 60_000
    this.staleMs = opts.staleMs ?? DEFAULT_STALE_MS
    this.now = opts.now ?? Date.now
  }

  private async fetch(): Promise<ConsoleEvent[]> {
    const now = this.now()
    const prs = await this.client.listPendingPrs()
    return prs.flatMap((p) => this.mapper(p, { now, staleMs: this.staleMs }))
  }

  async snapshot(_opts?: SnapshotOptions): Promise<ConsoleSnapshot> {
    void _opts
    const events = await this.fetch()
    for (const e of events) this.seen.add(e.id)
    return { events, cursor: null }
  }

  subscribe(h: SubscribeHandlers): () => void {
    let stopped = false
    h.onStatus('live')
    if (this.pollMs <= 0) return () => undefined
    const timer = setInterval(async () => {
      try {
        const fresh = (await this.fetch()).filter((e) => !this.seen.has(e.id))
        if (stopped) return
        for (const e of fresh) this.seen.add(e.id)
        if (fresh.length) h.onEvents(fresh)
      } catch {
        if (!stopped) h.onStatus('error')
      }
    }, this.pollMs)
    return () => {
      stopped = true
      clearInterval(timer)
    }
  }
}

/** Alert mapper: titles the inbox-only kinds, defers everything else to `base`. */
export function pendingPrAlertMapper(base: AlertMapper = defaultAlertMapper): AlertMapper {
  const verb: Record<string, string> = {
    [PR_KINDS.changesRequested]: 'changes requested',
    [PR_KINDS.conflicting]: 'has conflicts',
    [PR_KINDS.stale]: 'is stale',
  }
  return (e): Alert | null => {
    const v = verb[e.kind]
    if (!v) return base(e)
    const p = e.payload as { repo?: string; number?: number; title?: string }
    return {
      id: e.id,
      threadId: e.threadId,
      severity: e.severity,
      ts: e.ts,
      kind: e.kind,
      source: sourceOf(e),
      primary: linkOf(e),
      icon: 'pr',
      title: `${p.repo ?? 'PR'}${typeof p.number === 'number' ? `#${p.number}` : ''} ${v}`,
      detail: p.title,
    }
  }
}
