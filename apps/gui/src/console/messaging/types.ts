// Console messaging domain types. Independent of lib/types.ts on purpose: the
// adapter in adapter.ts maps these onto Tether mail envelopes. Additive only.

export type RecipientKind = 'agent' | 'user' | 'group'

export interface Recipient {
  /** Messaging URN, e.g. msg://agent/agent-mux/agt_pm01. Required to send. */
  address: string
  label: string
  kind: RecipientKind
  /** Group/broadcast recipients reach more than one party and need confirmation. */
  broadcast?: boolean
  /** Stable agent slug when known (used for per-agent rate limiting). */
  agentId?: string
}

export type MessageIntent = 'message' | 'nudge' | 'update_request'

export type DeliveryState = 'queued' | 'delivered' | 'read' | 'replied' | 'failed'

export const DELIVERY_ORDER: DeliveryState[] = ['queued', 'delivered', 'read', 'replied']

export interface ThreadMessage {
  id: string
  from: string
  to: string
  body: string
  subject?: string
  intent: MessageIntent
  threadId?: string
  inReplyTo?: string
  at: number
  state: DeliveryState
}

export interface SendRequest {
  to: Recipient
  /** Sender address; the client fills the signed-in operator when omitted. */
  from?: string
  intent: MessageIntent
  body: string
  subject?: string
  threadId?: string
  inReplyTo?: string
  /** Template id when the text came from one. */
  templateId?: string
  /** Request update only: what the sender expects back, e.g. "one line, within 10m". */
  expectedResponse?: string
  /** Must be true to send to a broadcast recipient. */
  confirmedBroadcast?: boolean
}

export interface SentMessage {
  id: string
  threadId: string
  state: DeliveryState
  sentAt: number
}

export interface ThreadQuery {
  /** Conversation with this address. */
  with?: string
  threadId?: string
  limit?: number
}

/**
 * Seam for console messaging. The mock implements it today; the live client
 * wraps Tether mail via Torque server routes (see BACKEND_NEEDS.md).
 */
export interface MessagingClient {
  /** Resolve an address/URN/agent slug to a sendable recipient, or null. */
  resolveRecipient(ref: string): Promise<Recipient | null>
  send(req: SendRequest): Promise<SentMessage>
  /** Current delivery state of a sent message. */
  getDelivery(id: string): Promise<DeliveryState>
  /** Recent messages, oldest first. */
  recentThread(q: ThreadQuery): Promise<ThreadMessage[]>
}
