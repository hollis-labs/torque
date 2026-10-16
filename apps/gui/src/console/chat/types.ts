// Console chat domain types. Independent of lib/types.ts: the mail adapter
// (mail.ts) maps onto MessagingClient, a future session-stream adapter maps
// the same ChatEvent union from Outpost/Nanite chat-stream frames. Event
// names here are placeholders; keep mappings in adapters, not components.
// Additive only. See BACKEND_NEEDS.md.

import type { DeliveryState, Recipient } from '../messaging'

/** How we can actually reach the agent right now. Shown in the UI, never hidden. */
export type ChatMode = 'live' | 'mail-only' | 'offline'

export interface ChatCapability {
  mode: ChatMode
  /** Why: "attached to session s-12 via Outpost hook", "no stream registered". */
  detail?: string
  /** Session id when attached to a hook-managed session. */
  sessionId?: string
}

export interface ChatThread {
  id: string
  agent: Recipient
  title: string
  capability: ChatCapability
  unread: number
  lastAt: number
  lastPreview?: string
}

export type ToolChipStatus = 'running' | 'ok' | 'error'

export interface ToolChip {
  id: string
  /** Tool or step name, e.g. "torque_task_get". */
  name: string
  status: ToolChipStatus
  /** Short, already-redacted summary. */
  detail?: string
}

export type ChatSender = 'operator' | 'agent' | 'system'

export interface ChatMessage {
  id: string
  threadId: string
  from: ChatSender
  body: string
  at: number
  /** Operator messages only. */
  delivery?: DeliveryState
  /** True while tokens are still arriving. */
  streaming?: boolean
  tools: ToolChip[]
  error?: string
}

/** Everything that can happen in a thread. Applied by `applyChatEvent`. */
export type ChatEvent =
  | { type: 'message'; message: ChatMessage }
  | { type: 'delta'; messageId: string; text: string }
  | { type: 'tool'; messageId: string; tool: ToolChip }
  | { type: 'end'; messageId: string; error?: string }
  | { type: 'delivery'; messageId: string; state: DeliveryState }
  | { type: 'capability'; capability: ChatCapability }

export interface HistoryQuery {
  /** Return messages older than this epoch ms (exclusive). */
  before?: number
  limit?: number
}

export interface HistoryPage {
  /** Oldest first. */
  messages: ChatMessage[]
  hasMore: boolean
}

/**
 * Seam for console chat. The mock implements it today; the mail impl wraps a
 * MessagingClient (no streaming); a session impl follows when the Nanite
 * chat-stream / Outpost hook lands (BACKEND_NEEDS.md).
 */
export interface ChatClient {
  listThreads(): Promise<ChatThread[]>
  /** Find or create the conversation with an agent. */
  openThread(agent: Recipient): Promise<ChatThread>
  history(threadId: string, q?: HistoryQuery): Promise<HistoryPage>
  /** Sends an operator message; further updates arrive through subscribe. */
  send(threadId: string, body: string): Promise<ChatMessage>
  markRead(threadId: string): Promise<void>
  /** Stream events for one thread. Returns an unsubscribe. */
  subscribe(threadId: string, listener: (e: ChatEvent) => void): () => void
  /** Thread list changes (new message, unread count). */
  subscribeThreads(listener: (threads: ChatThread[]) => void): () => void
}
