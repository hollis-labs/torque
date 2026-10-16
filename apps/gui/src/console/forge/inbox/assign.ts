// Assignment payload + bookkeeping for "assign this PR to an agent".
//
// Format (documented for the receiving agent in ASSIGNMENT.md): a plain-text
// message an agent can read as-is, ending in one fenced block tagged
// `console-pr-assignment` that holds the machine-readable v1 payload.
import type { ItemState } from '../../seams/state-store'
import { prKey, type PendingPrSummary } from '../types'
import { prWorkspaceHref } from './href'

export type AssignTask = 'review' | 'fix_checks' | 'address_comments' | 'resolve_conflicts'

export const ASSIGN_TASKS: { id: AssignTask; label: string; instructions: string[] }[] = [
  { id: 'review', label: 'Review', instructions: ['Review the diff and submit a review (approve, comment or request changes).', 'Do not merge.'] },
  { id: 'fix_checks', label: 'Fix failing checks', instructions: ['Find why the failing checks fail and fix it on the PR branch.', 'Push to the existing branch; do not open a new PR.', 'Report back when checks are green.'] },
  { id: 'address_comments', label: 'Address review comments', instructions: ['Work through the requested changes and open review threads.', 'Push fixes to the PR branch and reply on each thread.'] },
  { id: 'resolve_conflicts', label: 'Resolve conflicts', instructions: ['Rebase or merge the base branch into the PR branch and resolve conflicts.', 'Re-run checks after the push.'] },
]

export const ASSIGN_SCHEMA = 'console.pr_assignment/v1'
export const ASSIGN_FENCE = 'console-pr-assignment'

/** Task suggested for a PR from its reasons. */
export function suggestTask(p: Pick<PendingPrSummary, 'reasons'>): AssignTask {
  const has = (r: string) => p.reasons.includes(r)
  if (has('checks_failed')) return 'fix_checks'
  if (has('changes_requested')) return 'address_comments'
  if (has('conflicting')) return 'resolve_conflicts'
  return 'review'
}

export interface PrAssignmentPayload {
  schema: typeof ASSIGN_SCHEMA
  task: AssignTask
  /** Idempotency key: the same value means the same assignment. */
  dedupeKey: string
  pr: {
    host: string
    owner: string
    repo: string
    number: number
    title: string
    url?: string
    /** Console route of the PR workspace. */
    workspace: string
    author: string
  }
  reasons: string[]
  checks?: { passed: number; failed: number; pending: number }
  reviews?: { approved: number; changesRequested: number }
  mergeable: string
  instructions: string[]
  /** Free text from the operator. */
  note?: string
  assignedBy?: string
}

export const assignmentKey = (pr: string, agentId: string, task: AssignTask): string => `pr-assign:${pr}:${agentId}:${task}`
export const ASSIGN_KEY_PREFIX = 'pr-assign:'

const REASON_TEXT: Record<string, string> = {
  review_requested: 'review requested',
  checks_failed: 'checks failed',
  changes_requested: 'changes requested',
  conflicting: 'merge conflicts',
  unassigned: 'unassigned',
  stale: 'stale',
}

export function buildAssignmentPayload(
  p: PendingPrSummary,
  task: AssignTask,
  agentId: string,
  opts: { note?: string; assignedBy?: string } = {},
): PrAssignmentPayload {
  const def = ASSIGN_TASKS.find((t) => t.id === task) ?? ASSIGN_TASKS[0]
  const key = prKey(p.ref)
  return {
    schema: ASSIGN_SCHEMA,
    task,
    dedupeKey: assignmentKey(key, agentId, task),
    pr: {
      host: p.ref.repo.host,
      owner: p.ref.repo.owner,
      repo: p.ref.repo.name,
      number: p.ref.number,
      title: p.title,
      ...(p.url ? { url: p.url } : {}),
      workspace: prWorkspaceHref(p.ref),
      author: p.author.login,
    },
    reasons: [...p.reasons],
    ...(p.checksSummary ? { checks: p.checksSummary } : {}),
    ...(p.reviewSummary ? { reviews: p.reviewSummary } : {}),
    mergeable: p.mergeable,
    instructions: def.instructions,
    ...(opts.note?.trim() ? { note: opts.note.trim() } : {}),
    ...(opts.assignedBy ? { assignedBy: opts.assignedBy } : {}),
  }
}

export function assignmentSubject(payload: PrAssignmentPayload): string {
  const def = ASSIGN_TASKS.find((t) => t.id === payload.task)
  return `[PR] ${def?.label ?? payload.task}: ${payload.pr.owner}/${payload.pr.repo}#${payload.pr.number}`
}

/** Human-readable message body ending with the fenced machine payload. */
export function renderAssignmentBody(payload: PrAssignmentPayload): string {
  const { pr } = payload
  const def = ASSIGN_TASKS.find((t) => t.id === payload.task)
  const lines = [
    `You have been assigned a pull request: ${def?.label ?? payload.task}.`,
    '',
    `PR: ${pr.owner}/${pr.repo}#${pr.number} "${pr.title}" by ${pr.author}`,
    ...(pr.url ? [`URL: ${pr.url}`] : []),
    `Console workspace: ${pr.workspace}`,
    `Why it is pending: ${payload.reasons.map((r) => REASON_TEXT[r] ?? r).join(', ') || 'n/a'}`,
    ...(payload.checks ? [`Checks: ${payload.checks.passed} passed, ${payload.checks.failed} failed, ${payload.checks.pending} pending`] : []),
    ...(payload.reviews ? [`Reviews: ${payload.reviews.approved} approved, ${payload.reviews.changesRequested} requesting changes`] : []),
    `Mergeable: ${payload.mergeable}`,
    '',
    'What to do:',
    ...payload.instructions.map((i) => `- ${i}`),
    ...(payload.note ? ['', `Note from the operator: ${payload.note}`] : []),
    '',
    'Reply on this thread with a one-line result when done.',
    '',
    '```' + ASSIGN_FENCE,
    JSON.stringify(payload, null, 2),
    '```',
  ]
  return lines.join('\n')
}

/** Receiving-side helper: pull the v1 payload out of a message body, or null. */
export function parseAssignmentBody(body: string): PrAssignmentPayload | null {
  const m = new RegExp('```' + ASSIGN_FENCE + '\\s*\\n([\\s\\S]*?)\\n```').exec(body)
  if (!m) return null
  try {
    const v = JSON.parse(m[1]) as PrAssignmentPayload
    return v && v.schema === ASSIGN_SCHEMA ? v : null
  } catch {
    return null
  }
}

// ---- tracking (StateStore-backed) --------------------------------------

export interface AssignmentRecord {
  key: string
  prKey: string
  agentId: string
  task: AssignTask
  /** Messaging id of the dispatched assignment, when known. */
  messageId?: string
  at: number
  by?: string
}

/** Resolution stored on the settled assignment row. */
export const ASSIGNED_RESOLUTION = 'assigned'

export function encodeAssignmentNote(n: { messageId?: string; forgeAssigned?: boolean }): string {
  return JSON.stringify(n)
}

export function parseAssignmentState(s: ItemState): AssignmentRecord | null {
  if (!s.key.startsWith(ASSIGN_KEY_PREFIX) || s.status !== 'settled' || s.resolution !== ASSIGNED_RESOLUTION) return null
  // key = pr-assign:<prKey>:<agentId>:<task>; prKey itself contains ':' and '#', so split from the right.
  const rest = s.key.slice(ASSIGN_KEY_PREFIX.length)
  const parts = rest.split(':')
  if (parts.length < 3) return null
  const task = parts.pop() as AssignTask
  const agentId = parts.pop() as string
  let messageId: string | undefined
  try {
    messageId = (JSON.parse(s.note ?? '{}') as { messageId?: string }).messageId
  } catch {
    /* tolerant */
  }
  return { key: s.key, prKey: parts.join(':'), agentId, task, messageId, at: s.updatedAt, by: s.by }
}

export function assignmentsFor(pr: string, state: Record<string, ItemState>): AssignmentRecord[] {
  const prefix = `${ASSIGN_KEY_PREFIX}${pr}:`
  const out: AssignmentRecord[] = []
  for (const s of Object.values(state)) {
    if (!s.key.startsWith(prefix)) continue
    const r = parseAssignmentState(s)
    if (r) out.push(r)
  }
  return out.sort((a, b) => b.at - a.at)
}
