// ActionBus wiring for PR workspace writes. Comments and reviews both carry a
// `confirm` requirement (owner decision): nothing is posted as the user without
// a granted step-up. `onGranted` (params) fires after the grant and before the
// forge call, so the UI can show its optimistic placeholder only then.
// Handlers only ever see a granted confirmation; its token is handed to the
// HTTP client via withConfirmToken.
import type { ActionBus, ConsoleAction } from '../../seams/action-bus'
import type { ForgeClient } from '../client'
import { withConfirmToken } from '../confirm-token'
import { prKey, type AddCommentInput, type PrRef, type ReviewInput } from '../types'

export const WORKSPACE_ACTIONS = {
  comment: 'forge.pr.comment',
  review: 'forge.pr.review',
} as const

const subject = (ref: PrRef) => ({ type: 'forge.pr', id: prKey(ref), label: `${ref.repo.owner}/${ref.repo.name}#${ref.number}` })

export function commentAction(ref: PrRef, input: AddCommentInput, onGranted?: () => void): ConsoleAction {
  const s = subject(ref)
  return {
    kind: WORKSPACE_ACTIONS.comment,
    subject: s,
    params: { ref, input, onGranted },
    confirm: {
      what: {
        action: WORKSPACE_ACTIONS.comment,
        title: `${input.inReplyTo ? 'Reply on' : 'Comment on'} ${s.label}`,
        summary: input.path ? `Inline on ${input.path}${input.line != null ? `:${input.line}` : ''}` : 'Conversation comment',
        subject: s,
      },
      scope: ['forge.write'],
      ttlMs: 2 * 60_000,
    },
  }
}

const VERDICT_TITLE: Record<string, string> = {
  approve: 'Approve',
  request_changes: 'Request changes on',
  comment: 'Submit review on',
}

export function reviewAction(ref: PrRef, input: ReviewInput): ConsoleAction {
  const s = subject(ref)
  const n = input.comments?.length ?? 0
  return {
    kind: WORKSPACE_ACTIONS.review,
    subject: s,
    params: { ref, input },
    confirm: {
      what: {
        action: WORKSPACE_ACTIONS.review,
        title: `${VERDICT_TITLE[input.verdict] ?? 'Review'} ${s.label}`,
        summary: n ? `${n} inline comment${n === 1 ? '' : 's'}` : undefined,
        subject: s,
      },
      scope: input.verdict === 'comment' ? ['forge.write'] : ['forge.write', 'forge.review'],
      ttlMs: 2 * 60_000,
    },
  }
}

/** Registers both handlers on the bus; returns an unregister. */
export function registerWorkspaceActions(bus: ActionBus, client: ForgeClient): () => void {
  const offs = [
    bus.register(WORKSPACE_ACTIONS.comment, async (a, ctx) => {
      const { ref, input, onGranted } = a.params as { ref: PrRef; input: AddCommentInput; onGranted?: () => void }
      if (!ctx.confirmation) throw new Error('Missing step-up confirmation')
      onGranted?.()
      const data = await withConfirmToken(ctx.confirmation.token, () => client.addComment(ref, input))
      return { status: 'ok', data }
    }),
    bus.register(WORKSPACE_ACTIONS.review, async (a, ctx) => {
      const { ref, input } = a.params as { ref: PrRef; input: ReviewInput }
      if (!ctx.confirmation) throw new Error('Missing step-up confirmation')
      const data = await withConfirmToken(ctx.confirmation.token, () => client.reviewPr(ref, input))
      return { status: 'ok', data }
    }),
  ]
  return () => offs.forEach((o) => o())
}
