// ChatClient over Tether mail via M1's MessagingClient. Mail-only by nature:
// no token streaming, replies surface by polling the thread. A session-stream
// ChatClient replaces this per agent once the Nanite chat-stream lands.

import type { MessagingClient, Recipient, ThreadMessage } from '../messaging'
import type { ChatCapability, ChatClient, ChatEvent, ChatMessage, ChatThread } from './types'

export const MAIL_CAPABILITY: ChatCapability = { mode: 'mail-only', detail: 'Tether mail. Replies arrive when the agent next reads its inbox.' }

export function chatMessageFromThread(m: ThreadMessage, threadId: string, operator: string): ChatMessage {
  const mine = m.from === operator || m.from.startsWith('msg://user/')
  return { id: m.id, threadId, from: mine ? 'operator' : 'agent', body: m.body, at: m.at, ...(mine ? { delivery: m.state } : {}), tools: [] }
}

export interface MailChatOptions {
  operator: string
  /** Agents the panel can list. Threads are keyed by agent address. */
  agents: Recipient[]
  pollMs?: number
}

export function createMailChatClient(messaging: MessagingClient, opts: MailChatOptions): ChatClient {
  const pollMs = opts.pollMs ?? 4000
  const agents = new Map(opts.agents.map((a) => [a.address, a]))
  const seen = new Map<string, Set<string>>()
  const unread = new Map<string, number>()
  const threadId = (a: Recipient) => a.address
  const toThread = async (a: Recipient): Promise<ChatThread> => {
    const recent = await messaging.recentThread({ with: a.address, limit: 1 })
    const last = recent[recent.length - 1]
    return { id: threadId(a), agent: a, title: a.label, capability: MAIL_CAPABILITY, unread: unread.get(a.address) ?? 0, lastAt: last?.at ?? 0, lastPreview: last?.body.slice(0, 80) }
  }

  return {
    async listThreads() {
      return (await Promise.all([...agents.values()].map(toThread))).sort((a, b) => b.lastAt - a.lastAt)
    },
    async openThread(agent) {
      agents.set(agent.address, agent)
      return toThread(agent)
    },
    async history(id, q = {}) {
      const all = await messaging.recentThread({ with: id, limit: (q.limit ?? 20) + 1 + (q.before ? 200 : 0) })
      const older = all.filter((m) => q.before === undefined || m.at < q.before)
      const page = older.slice(-(q.limit ?? 20))
      // Gap: MessagingClient has no cursor; paging is approximate (BACKEND_NEEDS #2).
      return { messages: page.map((m) => chatMessageFromThread(m, id, opts.operator)), hasMore: older.length > page.length }
    },
    async send(id, body) {
      const to = agents.get(id)
      if (!to) throw new Error(`Unknown thread ${id}`)
      const out = await messaging.send({ to, intent: 'message', body })
      return { id: out.id, threadId: id, from: 'operator', body, at: out.sentAt, delivery: out.state, tools: [] }
    },
    async markRead(id) {
      unread.set(id, 0)
    },
    subscribe(id, listener) {
      const ids = seen.get(id) ?? new Set<string>()
      seen.set(id, ids)
      listener({ type: 'capability', capability: MAIL_CAPABILITY })
      let live = true
      const tick = async () => {
        try {
          const items = await messaging.recentThread({ with: id, limit: 30 })
          for (const m of items) {
            const cm = chatMessageFromThread(m, id, opts.operator)
            const isNew = !ids.has(m.id)
            ids.add(m.id)
            const e: ChatEvent = cm.from === 'operator' && !isNew ? { type: 'delivery', messageId: m.id, state: m.state } : { type: 'message', message: cm }
            if (live && (isNew || cm.from === 'operator')) listener(e)
            if (isNew && cm.from === 'agent') unread.set(id, (unread.get(id) ?? 0) + 1)
          }
        } catch {
          /* keep polling */
        }
      }
      void tick()
      const timer = setInterval(() => void tick(), pollMs)
      return () => {
        live = false
        clearInterval(timer)
      }
    },
    subscribeThreads() {
      return () => undefined // BACKEND_NEEDS #3: no push for unread yet
    },
  }
}
