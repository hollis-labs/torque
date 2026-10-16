// Maps console messaging onto Tether mail envelopes (lib/types MessageEnvelope).
// Pure helpers plus a thin client over the subset of ApiClient it needs, so the
// console never imports the full ApiClient. Backend gaps: see BACKEND_NEEDS.md.

import type { ListPage, MessageEnvelope, MessageKind, SendMessageRequest } from '@/lib/types'
import { messageBody, messageSubject, parseUrn } from '@/lib/messaging'
import { OPERATOR_ADDRESS } from './mock'
import type { DeliveryState, MessageIntent, MessagingClient, SendRequest, ThreadMessage } from './types'

const KIND_FOR_INTENT: Record<MessageIntent, MessageKind> = {
  message: 'notice',
  nudge: 'notice',
  update_request: 'request',
}

export function toEnvelopeRequest(req: SendRequest, from = OPERATOR_ADDRESS): SendMessageRequest {
  const metadata: Record<string, string> = { 'console.intent': req.intent }
  if (req.templateId) metadata['console.template'] = req.templateId
  if (req.expectedResponse) metadata['console.expected_response'] = req.expectedResponse
  return {
    kind: KIND_FOR_INTENT[req.intent],
    from: req.from ?? from,
    to: req.to.address,
    thread_id: req.threadId,
    in_reply_to: req.inReplyTo,
    payload: { ...(req.subject ? { subject: req.subject } : {}), body: req.body, ...(req.expectedResponse ? { expected_response: req.expectedResponse } : {}) },
    metadata,
  }
}

/** consumed => read; a later inbound envelope in the same thread => replied. */
export function deliveryFromEnvelope(m: MessageEnvelope, thread: MessageEnvelope[] = []): DeliveryState {
  const answered = thread.some((o) => o.in_reply_to === m.id || (o.from === m.to && Date.parse(o.created_at) > Date.parse(m.created_at)))
  if (answered) return 'replied'
  if (m.consumed_at) return 'read'
  if (m.delivered_at) return 'delivered'
  return 'queued'
}

export function threadMessageFromEnvelope(m: MessageEnvelope, thread: MessageEnvelope[] = []): ThreadMessage {
  const intent = (m.metadata?.['console.intent'] as MessageIntent | undefined) ?? (m.kind === 'request' ? 'update_request' : 'message')
  return {
    id: m.id, from: m.from, to: m.to, body: messageBody(m), subject: messageSubject(m) !== messageBody(m) ? messageSubject(m) : undefined,
    intent, threadId: m.thread_id || m.id, inReplyTo: m.in_reply_to, at: Date.parse(m.created_at), state: deliveryFromEnvelope(m, thread),
  }
}

/** The slice of ApiClient this adapter uses. */
export interface MailApi {
  sendMessage(req: SendMessageRequest): Promise<MessageEnvelope>
  getMessage(id: string): Promise<MessageEnvelope>
  getThread(threadId: string, filter?: { limit?: number }): Promise<ListPage<MessageEnvelope>>
}

export function createMailMessagingClient(api: MailApi, opts: { from?: string } = {}): MessagingClient {
  return {
    async resolveRecipient(ref) {
      const p = parseUrn(ref.trim())
      if (!p) return null
      return { address: ref.trim(), label: p.id, kind: p.kind === 'user' ? 'user' : p.kind === 'group' ? 'group' : 'agent', broadcast: p.kind === 'group', agentId: p.kind === 'agent' ? p.id : undefined }
    },
    async send(req) {
      const env = await api.sendMessage(toEnvelopeRequest(req, opts.from))
      return { id: env.id, threadId: env.thread_id || env.id, state: deliveryFromEnvelope(env), sentAt: Date.parse(env.created_at) }
    },
    async getDelivery(id) {
      const env = await api.getMessage(id)
      const page = await api.getThread(env.thread_id || env.id)
      return deliveryFromEnvelope(env, page.items ?? [])
    },
    async recentThread(q) {
      if (!q.threadId) return []
      const page = await api.getThread(q.threadId, { limit: q.limit })
      const items = page.items ?? []
      return items.map((m) => threadMessageFromEnvelope(m, items)).sort((a, b) => a.at - b.at)
    },
  }
}
