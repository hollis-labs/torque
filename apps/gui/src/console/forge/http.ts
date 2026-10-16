// Typed HTTP implementation of ForgeClient against the PROPOSED Torque forge
// REST surface specified in BACKEND_REQUIREMENTS.md. The browser talks only to
// the Torque API; Torque fronts the Cerberus forge plugin. No tokens here.
//
// Wire format: JSON, camelCase, identical to ./types.ts. Responses are wrapped
// as `{ "data": ... }` (a bare body is also accepted). Unknown fields are
// passed through untouched; missing collection fields are defaulted.

import { GUI_CLIENT_HEADERS } from '@/lib/api'
import type { ForgeClient } from './client'
import {
  ForgeError,
  type AddCommentInput,
  type AssignInput,
  type Commit,
  type FileDiff,
  type ForgeComment,
  type ForgeErrorCode,
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

export const DEFAULT_FORGE_BASE = '/api/v1/forge'

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

export interface HttpForgeClientOptions {
  /** Base path or absolute URL; default `/api/v1/forge`. Trailing slash ignored. */
  baseUrl?: string
  /** Injectable for tests; defaults to global fetch. */
  fetch?: FetchLike
  /**
   * Supplies the IdentityConfirm proof token for write ops (sent as
   * `X-Identity-Confirm`). Resolve it after running IdentityConfirm; return
   * undefined when none is available (the backend will answer `unauthorized`).
   */
  confirmToken?: () => string | undefined | Promise<string | undefined>
  /** Extra headers (e.g. auth in non-same-origin setups). */
  headers?: Record<string, string>
  /** Idempotency key generator for writes. */
  idempotencyKey?: () => string
}

/** Map Cerberus plugin codes and HTTP statuses onto ForgeErrorCode. */
export function mapForgeErrorCode(code: string | undefined, status: number): ForgeErrorCode {
  switch (code) {
    case 'not_found':
    case 'invalid_args':
    case 'unauthorized':
    case 'unavailable':
    case 'conflict':
    case 'rate_limited':
      return code
    case 'credential_missing':
    case 'identity_required':
    case 'forbidden':
      return 'unauthorized'
    case 'connector_unavailable':
    case 'operation_failed':
      return 'unavailable'
  }
  if (code) return code // unknown backend codes pass through (open union)
  if (status === 400 || status === 422) return 'invalid_args'
  if (status === 401 || status === 403) return 'unauthorized'
  if (status === 404) return 'not_found'
  if (status === 409) return 'conflict'
  if (status === 429) return 'rate_limited'
  return 'unavailable'
}

const enc = encodeURIComponent

function repoPath(r: RepoRef): string {
  return `/repos/${enc(r.host)}/${enc(r.owner)}/${enc(r.name)}`
}

function prPath(p: PrRef): string {
  return `${repoPath(p.repo)}/pulls/${p.number}`
}

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function unwrap(body: unknown): unknown {
  return isObj(body) && 'data' in body ? body.data : body
}

function listOf<T>(v: unknown): T[] {
  if (Array.isArray(v)) return v as T[]
  if (isObj(v) && Array.isArray(v.items)) return v.items as T[]
  return []
}

function normalizePr(v: unknown): PullRequest {
  const p = (isObj(v) ? v : {}) as unknown as PullRequest
  return { ...p, assignees: p.assignees ?? [], checks: p.checks ?? [], reviews: p.reviews ?? [], mergeable: p.mergeable ?? 'unknown' }
}

function normalizePending(v: unknown): PendingPrSummary {
  const p = (isObj(v) ? v : {}) as unknown as PendingPrSummary
  return { ...p, assignees: p.assignees ?? [], reasons: p.reasons ?? [], mergeable: p.mergeable ?? 'unknown' }
}

function normalizeFile(v: unknown): FileDiff {
  const f = (isObj(v) ? v : {}) as unknown as FileDiff
  return { ...f, hunks: f.hunks ?? [], additions: f.additions ?? 0, deletions: f.deletions ?? 0 }
}

export class HttpForgeClient implements ForgeClient {
  private readonly base: string
  private readonly doFetch: FetchLike
  private readonly opts: HttpForgeClientOptions

  constructor(opts: HttpForgeClientOptions = {}) {
    this.opts = opts
    this.base = (opts.baseUrl ?? DEFAULT_FORGE_BASE).replace(/\/+$/, '')
    this.doFetch = opts.fetch ?? ((i, init) => globalThis.fetch(i, init))
  }

  private async request(method: 'GET' | 'POST' | 'PUT', path: string, body?: unknown, write = false): Promise<unknown> {
    const headers: Record<string, string> = { Accept: 'application/json', ...GUI_CLIENT_HEADERS, ...this.opts.headers }
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    if (write) {
      const tok = await this.opts.confirmToken?.()
      if (tok) headers['X-Identity-Confirm'] = tok
      const key = this.opts.idempotencyKey?.()
      if (key) headers['Idempotency-Key'] = key
    }
    let res: Response
    try {
      res = await this.doFetch(this.base + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
    } catch (e) {
      throw new ForgeError('unavailable', e instanceof Error ? e.message : 'network error')
    }
    let parsed: unknown
    const text = await res.text().catch(() => '')
    if (text) {
      try {
        parsed = JSON.parse(text)
      } catch {
        if (res.ok) throw new ForgeError('unavailable', 'invalid JSON from forge API')
      }
    }
    if (!res.ok) {
      const e = isObj(parsed) && isObj(parsed.error) ? parsed.error : isObj(parsed) ? parsed : {}
      const code = typeof e.code === 'string' ? e.code : undefined
      const msg = typeof e.message === 'string' ? e.message : `forge API ${res.status}`
      throw new ForgeError(mapForgeErrorCode(code, res.status), msg)
    }
    return unwrap(parsed)
  }

  async getRepo(repo: RepoRef): Promise<Repo> {
    const r = (await this.request('GET', repoPath(repo))) as Repo
    return { ...repo, ...r }
  }

  async listPrs(query: ListPrsQuery = {}): Promise<Page<PullRequest>> {
    const q = new URLSearchParams()
    if (query.state) q.set('state', query.state)
    if (query.author) q.set('author', query.author)
    if (query.assignee) q.set('assignee', query.assignee)
    if (query.query) q.set('q', query.query)
    if (query.limit) q.set('limit', String(query.limit))
    if (query.cursor) q.set('cursor', query.cursor)
    const qs = q.toString()
    const path = (query.repo ? `${repoPath(query.repo)}/pulls` : '/pulls') + (qs ? `?${qs}` : '')
    const data = await this.request('GET', path)
    return {
      items: listOf<unknown>(data).map(normalizePr),
      nextCursor: isObj(data) && typeof data.nextCursor === 'string' ? data.nextCursor : null,
    }
  }

  async getPr(ref: PrRef): Promise<PullRequest> {
    return normalizePr(await this.request('GET', prPath(ref)))
  }

  async getDiff(ref: PrRef): Promise<FileDiff[]> {
    return listOf<unknown>(await this.request('GET', `${prPath(ref)}/diff`)).map(normalizeFile)
  }

  async listCommits(ref: PrRef): Promise<Commit[]> {
    return listOf<Commit>(await this.request('GET', `${prPath(ref)}/commits`))
  }

  async listComments(ref: PrRef): Promise<ForgeComment[]> {
    return listOf<ForgeComment>(await this.request('GET', `${prPath(ref)}/comments`))
  }

  async addComment(ref: PrRef, input: AddCommentInput): Promise<ForgeComment> {
    return (await this.request('POST', `${prPath(ref)}/comments`, input, true)) as ForgeComment
  }

  async reviewPr(ref: PrRef, input: ReviewInput): Promise<Review> {
    return (await this.request('POST', `${prPath(ref)}/reviews`, input, true)) as Review
  }

  async assign(ref: PrRef, input: AssignInput): Promise<PullRequest> {
    return normalizePr(await this.request('PUT', `${prPath(ref)}/assignees`, input, true))
  }

  async listPendingPrs(): Promise<PendingPrSummary[]> {
    return listOf<unknown>(await this.request('GET', '/pulls/pending')).map(normalizePending)
  }
}
