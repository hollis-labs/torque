// Data-driven event -> card rules. Event kinds are placeholders in F1, so the
// kind -> card mapping lives only in rule data (DEFAULT_CARD_RULES) and can be
// replaced wholesale (or loaded from config) without touching components.
import type { ConsoleSeverity } from '../events/types'
import type { ActionIntent, CardActionSpec, CardKindId, CardSurface } from './types'

export interface RuleMatch {
  /** Exact kinds. */
  kinds?: string[]
  kindPrefix?: string
  minSeverity?: ConsoleSeverity
  subjectType?: string
  /** Every entry must equal the payload value at that key (shallow). */
  payloadEquals?: Record<string, string | number | boolean>
}

export interface RuleActionDecl {
  id: string
  label: string
  intent?: ActionIntent
  kind: string
  params?: Record<string, unknown>
  /** Shorthand: require IdentityConfirm with these scopes. */
  confirmScope?: string[]
  settlesAs?: string
}

export interface CardRule {
  id: string
  match: RuleMatch
  card: {
    kind: CardKindId
    surfaces: CardSurface[]
    /** Template; events producing the same key coalesce into one card. */
    dedupeKey: string
    title: string
    summary?: string
    /** Fixed severity, or "event" (default) to take the event's. */
    severity?: ConsoleSeverity | 'event'
    ttlMs?: number
    /** Payload key holding an epoch-ms expiry; used when no ttlMs is set. */
    expiresAtPayload?: string
    /** Key templates of other cards this card replaces (they become phase "superseded"). */
    supersedes?: string[]
    /** "offered": actions from the event, "declared": only `actions`, "both" (default). */
    actionsFrom?: 'offered' | 'declared' | 'both'
    actions?: RuleActionDecl[]
    props?: Record<string, unknown>
  }
  /** Later events with the same dedupe key replace earlier ones (default true). Set false to keep only the first. */
  latestWins?: boolean
}

/** Closes cards: an event matching `match` resolves the card whose dedupe key equals `key`. */
export interface ResolveRule {
  id: string
  match: RuleMatch
  key: string
  label: string
}

export interface CardRuleSet {
  rules: CardRule[]
  resolvers?: ResolveRule[]
}

export const DEFAULT_CARD_RULES: CardRuleSet = {
  rules: [
    {
      id: 'approval',
      match: { kinds: ['approval.requested'] },
      card: {
        kind: 'approval',
        surfaces: ['tray', 'modal', 'inline', 'toast'],
        dedupeKey: 'approval:{correlationId|id}',
        title: '{payload.title}',
        summary: '{payload.summary}',
        expiresAtPayload: 'expiresAt',
        actionsFrom: 'both',
        actions: [
          { id: 'approve', label: 'Approve', intent: 'primary', kind: 'approve', confirmScope: ['approval:approve'], settlesAs: 'approved' },
          { id: 'reject', label: 'Reject', intent: 'danger', kind: 'reject', settlesAs: 'rejected' },
        ],
      },
    },
    {
      id: 'agent-status',
      match: { kinds: ['agent.state'] },
      card: {
        kind: 'status',
        surfaces: ['inline', 'tray'],
        dedupeKey: 'agent:{subject.agent}',
        title: '{subject.agent.label}',
        summary: '{payload.state}',
        actionsFrom: 'offered',
      },
    },
    {
      id: 'task-status',
      match: { kinds: ['task.state_changed'] },
      card: {
        kind: 'status',
        surfaces: ['inline', 'tray'],
        dedupeKey: 'task:{subject.task}',
        title: '{payload.title|subject.task.label}',
        summary: '{payload.from} -> {payload.to}',
        actionsFrom: 'offered',
      },
    },
    {
      id: 'message',
      match: { kinds: ['message.received'] },
      card: {
        kind: 'info',
        surfaces: ['toast', 'tray', 'inline'],
        dedupeKey: 'thread:{threadId|id}',
        title: '{payload.subject|payload.from}',
        summary: '{payload.preview}',
        actionsFrom: 'offered',
      },
    },
    {
      id: 'pr-attention',
      match: { kindPrefix: 'pr.' },
      card: {
        kind: 'info',
        surfaces: ['toast', 'tray', 'inline'],
        dedupeKey: 'pr:{subject.pr}',
        title: '{payload.title|subject.pr.label}',
        summary: '{kind}',
        actionsFrom: 'offered',
        ttlMs: 24 * 3_600_000,
      },
    },
  ],
  resolvers: [
    { id: 'approval-resolved', match: { kinds: ['approval.resolved'] }, key: 'approval:{correlationId|id}', label: 'Resolved' },
    { id: 'pr-merged', match: { kinds: ['pr.merged'] }, key: 'pr:{subject.pr}', label: 'Merged' },
  ],
}

export function offerToSpec(o: { id: string; label: string; kind: string; requiresConfirm?: boolean; scope?: string[]; params?: Record<string, unknown> }): CardActionSpec {
  const intent: ActionIntent = o.kind === 'approve' ? 'primary' : o.kind === 'reject' ? 'danger' : 'neutral'
  return {
    id: o.id,
    label: o.label,
    intent,
    kind: o.kind,
    params: o.params,
    origin: 'offered',
    ...(o.requiresConfirm ? { confirm: { what: { action: `${o.kind}:${o.id}`, title: o.label }, scope: o.scope ?? [] } } : {}),
  }
}
