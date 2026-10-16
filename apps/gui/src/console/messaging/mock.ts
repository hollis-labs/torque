import { parseUrn } from '@/lib/messaging'
import type { DeliveryState, MessagingClient, Recipient, SendRequest, SentMessage, ThreadMessage } from './types'

export const OPERATOR_ADDRESS = 'msg://user/local/operator'

export const MOCK_RECIPIENTS: Recipient[] = [
  { address: 'msg://agent/agent-mux/agt_pm01', label: 'Portfolio Manager', kind: 'agent', agentId: 'portfolio-manager' },
  { address: 'msg://agent/agent-mux/agt_tw02', label: 'Team Worker', kind: 'agent', agentId: 'team-worker' },
  { address: 'msg://group/local/all-agents', label: 'All agents', kind: 'group', broadcast: true },
]

export interface MockMessagingOptions {
  now?: () => number
  /** ms after send before a message shows delivered / read. */
  deliveredAfterMs?: number
  readAfterMs?: number
  seed?: ThreadMessage[]
  /** Addresses whose sends fail (to exercise the failed state). */
  failFor?: string[]
}

export interface MockMessagingClient extends MessagingClient {
  /** Test/demo hook: simulate the recipient replying. */
  markReplied(id: string): void
  sent(): ThreadMessage[]
}

export function createMockMessagingClient(opts: MockMessagingOptions = {}): MockMessagingClient {
  const now = opts.now ?? Date.now
  const deliveredAfter = opts.deliveredAfterMs ?? 800
  const readAfter = opts.readAfterMs ?? 2500
  const msgs: ThreadMessage[] = [...(opts.seed ?? [])]
  const replied = new Set<string>()
  const failed = new Set<string>()
  let seq = 0

  const stateOf = (m: ThreadMessage): DeliveryState => {
    if (failed.has(m.id)) return 'failed'
    if (replied.has(m.id)) return 'replied'
    if (m.state === 'replied' || m.state === 'read') return m.state
    const age = now() - m.at
    return age >= readAfter ? 'read' : age >= deliveredAfter ? 'delivered' : 'queued'
  }

  return {
    async resolveRecipient(ref) {
      const r = ref.trim()
      const hit = MOCK_RECIPIENTS.find((x) => x.address === r || x.agentId === r)
      if (hit) return hit
      const p = parseUrn(r)
      return p && p.kind === 'agent' ? { address: r, label: p.id, kind: 'agent', agentId: p.id } : null
    },
    async send(req: SendRequest): Promise<SentMessage> {
      const id = `mock-msg-${++seq}`
      const at = now()
      const threadId = req.threadId ?? req.inReplyTo ?? id
      msgs.push({ id, from: req.from ?? OPERATOR_ADDRESS, to: req.to.address, body: req.body, subject: req.subject, intent: req.intent, threadId, inReplyTo: req.inReplyTo, at, state: 'queued' })
      if (opts.failFor?.includes(req.to.address)) failed.add(id)
      return { id, threadId, state: failed.has(id) ? 'failed' : 'queued', sentAt: at }
    },
    async getDelivery(id) {
      const m = msgs.find((x) => x.id === id)
      return m ? stateOf(m) : 'failed'
    },
    async recentThread(q) {
      const out = msgs
        .filter((m) => (q.threadId ? m.threadId === q.threadId : q.with ? m.to === q.with || m.from === q.with : true))
        .sort((a, b) => a.at - b.at)
        .map((m) => ({ ...m, state: stateOf(m) }))
      return out.slice(-(q.limit ?? 5))
    },
    markReplied: (id) => void replied.add(id),
    sent: () => msgs.map((m) => ({ ...m, state: stateOf(m) })),
  }
}
