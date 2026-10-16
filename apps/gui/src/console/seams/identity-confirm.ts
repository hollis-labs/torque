// IdentityConfirm: the step-up seam in front of every approve/apply that
// authorises a change. The real implementation will call the auth workstream's
// API (undefined at the time of writing); the request/result shape below is the
// stable part: who, what, scope, expiry, result.

import type { ConsoleRef } from '../events/types'

export interface ConfirmWho {
  id: string
  label?: string
}

export interface ConfirmWhat {
  /** Machine-readable action, e.g. "agent.apply" or "approval.approve". */
  action: string
  /** Human-readable one-liner shown in the confirm prompt. */
  title: string
  summary?: string
  subject?: ConsoleRef
}

export interface ConfirmRequest {
  /** Defaults to the signed-in user when omitted. */
  who?: ConfirmWho
  what: ConfirmWhat
  /** Permission scopes the grant must cover. */
  scope: string[]
  /** How long a grant stays valid; implementations may shorten it. */
  ttlMs?: number
}

export type ConfirmOutcome = 'granted' | 'denied' | 'cancelled' | 'expired' | 'unavailable'

export interface ConfirmResult {
  outcome: ConfirmOutcome
  who: ConfirmWho
  what: ConfirmWhat
  scope: string[]
  /** Epoch ms when the grant lapses (granted only). */
  expiresAt: number | null
  issuedAt: number
  /** Opaque proof the backend verifies when the action executes. Never log it. */
  token?: string
  reason?: string
}

export interface IdentityConfirm {
  confirm(req: ConfirmRequest): Promise<ConfirmResult>
}

export function isGranted(r: ConfirmResult, now = Date.now()): boolean {
  return r.outcome === 'granted' && (r.expiresAt === null || r.expiresAt > now)
}

export type MockConfirmMode = 'auto-grant' | 'auto-deny' | 'manual'

export interface PendingConfirm {
  id: string
  request: ConfirmRequest
}

export interface MockIdentityConfirmOptions {
  mode?: MockConfirmMode
  who?: ConfirmWho
  now?: () => number
  defaultTtlMs?: number
}

/**
 * Mock step-up. 'auto-grant'/'auto-deny' resolve immediately (tests, fixtures);
 * 'manual' queues the request so a dialog component can render `pending()` and
 * call `resolve(id, outcome)`; the UI is a later task's concern, this is its hook.
 */
export class MockIdentityConfirm implements IdentityConfirm {
  mode: MockConfirmMode
  private readonly who: ConfirmWho
  private readonly now: () => number
  private readonly ttl: number
  private seq = 0
  private queue = new Map<string, { req: ConfirmRequest; done: (r: ConfirmResult) => void }>()
  private listeners = new Set<() => void>()
  private snapshot: PendingConfirm[] = []

  constructor(opts: MockIdentityConfirmOptions = {}) {
    this.mode = opts.mode ?? 'auto-grant'
    this.who = opts.who ?? { id: 'mock-user', label: 'Mock User' }
    this.now = opts.now ?? Date.now
    this.ttl = opts.defaultTtlMs ?? 60_000
  }

  private result(req: ConfirmRequest, outcome: ConfirmOutcome, reason?: string): ConfirmResult {
    const issuedAt = this.now()
    const granted = outcome === 'granted'
    return {
      outcome,
      who: req.who ?? this.who,
      what: req.what,
      scope: req.scope,
      issuedAt,
      expiresAt: granted ? issuedAt + (req.ttlMs ?? this.ttl) : null,
      ...(granted ? { token: `mock-${issuedAt}-${++this.seq}` } : {}),
      ...(reason ? { reason } : {}),
    }
  }

  confirm(req: ConfirmRequest): Promise<ConfirmResult> {
    if (this.mode === 'auto-grant') return Promise.resolve(this.result(req, 'granted'))
    if (this.mode === 'auto-deny') return Promise.resolve(this.result(req, 'denied', 'mock policy'))
    return new Promise((done) => {
      this.queue.set(`c${++this.seq}`, { req, done })
      this.publish()
    })
  }

  pending(): PendingConfirm[] {
    return this.snapshot
  }

  resolve(id: string, outcome: ConfirmOutcome, reason?: string): void {
    const q = this.queue.get(id)
    if (!q) return
    this.queue.delete(id)
    q.done(this.result(q.req, outcome, reason))
    this.publish()
  }

  /** useSyncExternalStore-compatible. */
  subscribe = (l: () => void): (() => void) => {
    this.listeners.add(l)
    return () => this.listeners.delete(l)
  }

  private publish() {
    this.snapshot = [...this.queue].map(([id, q]) => ({ id, request: q.req }))
    this.listeners.forEach((l) => l())
  }
}
