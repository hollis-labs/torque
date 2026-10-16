import type { Recipient } from '../messaging'
import type { ChatCapability, ChatClient, ChatEvent, ChatMessage, ChatThread, HistoryPage, ToolChip } from './types'

export const MOCK_CHAT_AGENTS: { agent: Recipient; capability: ChatCapability }[] = [
  {
    agent: { address: 'msg://agent/agent-mux/agt_pm01', label: 'Portfolio Manager', kind: 'agent', agentId: 'portfolio-manager' },
    capability: { mode: 'live', detail: 'Attached to session s-pm-01 via Outpost hook', sessionId: 's-pm-01' },
  },
  {
    agent: { address: 'msg://agent/agent-mux/agt_tw02', label: 'Team Worker', kind: 'agent', agentId: 'team-worker' },
    capability: { mode: 'mail-only', detail: 'No stream registered. Replies arrive by mail and may be delayed.' },
  },
  {
    agent: { address: 'msg://agent/agent-mux/agt_rc03', label: 'Release Captain', kind: 'agent', agentId: 'release-captain' },
    capability: { mode: 'offline', detail: 'No live session. Messages queue until it connects.' },
  },
]

interface Reply { text: string; tools: Omit<ToolChip, 'id'>[] }

/** Streaming-style fixtures; text mixes refs so Linkified has something to chip. */
export const MOCK_REPLIES: Reply[] = [
  {
    text: 'Checked CW-20261016-0117. The chat panel is on fixtures and mail; blocked on nothing. See `CW-20261016-0001` for the sample and @claude-code for review.',
    tools: [{ name: 'torque_task_get', status: 'ok', detail: 'CW-20261016-0117' }],
  },
  {
    text: 'Running the suite now. Two files left, then I will post the result to EP-20261016-0002.\n\nNo failures so far.',
    tools: [{ name: 'bash', status: 'ok', detail: 'npx vitest run' }, { name: 'torque_comment_add', status: 'running' }],
  },
  { text: 'Understood. I will pick that up next.', tools: [] },
]

const HOUR = 3_600_000

export interface MockChatOptions {
  now?: () => number
  schedule?: (fn: () => void, ms: number) => () => void
  /** ms between streamed tokens. */
  tokenMs?: number
  /** Think time before the first token (live) or the whole reply (mail-only). */
  replyAfterMs?: number
  /** Seed history length for the first thread, for paging. */
  historyCount?: number
  agents?: typeof MOCK_CHAT_AGENTS
}

export interface MockChatClient extends ChatClient {
  /** Test/demo hook: an agent message arrives unprompted. */
  inject(threadId: string, body: string): void
  sentBodies(): string[]
}

const defaultSchedule = (fn: () => void, ms: number) => {
  const t = setTimeout(fn, ms)
  return () => clearTimeout(t)
}

export function createMockChatClient(opts: MockChatOptions = {}): MockChatClient {
  const now = opts.now ?? Date.now
  const schedule = opts.schedule ?? defaultSchedule
  const tokenMs = opts.tokenMs ?? 40
  const replyAfter = opts.replyAfterMs ?? 600
  const agents = opts.agents ?? MOCK_CHAT_AGENTS
  const t0 = now()
  let seq = 0
  let replySeq = 0
  const nid = () => `mock-chat-${++seq}`

  const threads = new Map<string, ChatThread>()
  const msgs = new Map<string, ChatMessage[]>()
  const subs = new Map<string, Set<(e: ChatEvent) => void>>()
  const listSubs = new Set<(t: ChatThread[]) => void>()
  const sent: string[] = []

  const emitThreads = () => {
    const list = snapshot()
    listSubs.forEach((l) => l(list))
  }
  const snapshot = () => [...threads.values()].sort((a, b) => b.lastAt - a.lastAt).map((t) => ({ ...t }))
  const emit = (threadId: string, e: ChatEvent) => subs.get(threadId)?.forEach((l) => l(e))

  const addThread = (agent: Recipient, capability: ChatCapability): ChatThread => {
    const t: ChatThread = { id: `thread-${agent.agentId ?? agent.address}`, agent, title: agent.label, capability, unread: 0, lastAt: t0 }
    threads.set(t.id, t)
    msgs.set(t.id, [])
    return t
  }
  const touch = (id: string, m: ChatMessage, unread: boolean) => {
    const t = threads.get(id)
    if (!t) return
    t.lastAt = m.at
    t.lastPreview = m.body.slice(0, 80)
    if (unread) t.unread += 1
  }
  const push = (m: ChatMessage, unread = false) => {
    msgs.get(m.threadId)?.push(m)
    touch(m.threadId, m, unread)
  }

  agents.forEach(({ agent, capability }) => addThread(agent, capability))

  // Seed: a long history on the first thread (paging), a short one with an unread reply on the second.
  const first = [...threads.values()][0]
  if (first) {
    const n = opts.historyCount ?? 40
    for (let i = 0; i < n; i++) {
      const mine = i % 2 === 0
      push({ id: `seed-${i}`, threadId: first.id, from: mine ? 'operator' : 'agent', body: mine ? `Status check ${i / 2 + 1}?` : `Update ${Math.ceil(i / 2)}: on track, tracking CW-20261016-0100.`, at: t0 - (n - i) * 10 * 60_000 - HOUR, delivery: mine ? 'replied' : undefined, tools: [] })
    }
  }
  const second = [...threads.values()][1]
  if (second) {
    push({ id: 'seed-tw-1', threadId: second.id, from: 'operator', body: 'Can you pick up CW-20261016-0118 next?', at: t0 - 30 * 60_000, delivery: 'read', tools: [] })
    push({ id: 'seed-tw-2', threadId: second.id, from: 'agent', body: 'Yes. Starting after the current PR merges.', at: t0 - 12 * 60_000, tools: [] }, true)
  }

  const streamReply = (threadId: string, mode: ChatCapability['mode']) => {
    const reply = MOCK_REPLIES[replySeq++ % MOCK_REPLIES.length]
    const id = nid()
    if (mode === 'offline') return
    if (mode === 'mail-only') {
      schedule(() => {
        const m: ChatMessage = { id, threadId, from: 'agent', body: reply.text, at: now(), tools: [] }
        push(m, true)
        emit(threadId, { type: 'message', message: m })
        emitThreads()
      }, replyAfter * 4)
      return
    }
    schedule(() => {
      const m: ChatMessage = { id, threadId, from: 'agent', body: '', at: now(), streaming: true, tools: [] }
      push(m, true)
      emit(threadId, { type: 'message', message: m })
      reply.tools.forEach((t, i) => {
        const chip: ToolChip = { ...t, id: `${id}-t${i}` }
        schedule(() => emit(threadId, { type: 'tool', messageId: id, tool: { ...chip, status: 'running' } }), tokenMs * (i + 1))
        if (t.status !== 'running') schedule(() => emit(threadId, { type: 'tool', messageId: id, tool: chip }), tokenMs * (i + 3))
      })
      const tokens = reply.text.match(/\S+\s*/g) ?? []
      let acc = ''
      tokens.forEach((tok, i) => {
        schedule(() => {
          acc += tok
          const stored = msgs.get(threadId)?.find((x) => x.id === id)
          if (stored) stored.body = acc
          emit(threadId, { type: 'delta', messageId: id, text: tok })
          if (i === tokens.length - 1) {
            if (stored) stored.streaming = false
            reply.tools.forEach((t, k) => stored && (stored.tools[k] = { ...t, id: `${id}-t${k}` } as ToolChip))
            emit(threadId, { type: 'end', messageId: id })
            emitThreads()
          }
        }, tokenMs * (i + 1))
      })
    }, replyAfter)
  }

  return {
    async listThreads() {
      return snapshot()
    },
    async openThread(agent) {
      const hit = [...threads.values()].find((t) => t.agent.address === agent.address)
      if (hit) return { ...hit }
      const t = addThread(agent, { mode: 'mail-only', detail: 'Not in the live roster. Replies arrive by mail.' })
      emitThreads()
      return { ...t }
    },
    async history(threadId, q = {}) {
      const all = (msgs.get(threadId) ?? []).filter((m) => q.before === undefined || m.at < q.before).sort((a, b) => a.at - b.at)
      const limit = q.limit ?? 20
      const page = all.slice(-limit)
      return { messages: page.map((m) => ({ ...m, tools: [...m.tools] })), hasMore: all.length > page.length } satisfies HistoryPage
    },
    async send(threadId, body) {
      const t = threads.get(threadId)
      if (!t) throw new Error(`Unknown thread ${threadId}`)
      sent.push(body)
      const m: ChatMessage = { id: nid(), threadId, from: 'operator', body, at: now(), delivery: 'queued', tools: [] }
      push(m)
      emitThreads()
      if (t.capability.mode === 'offline') return m
      schedule(() => emit(threadId, { type: 'delivery', messageId: m.id, state: 'delivered' }), 200)
      schedule(() => {
        const stored = msgs.get(threadId)?.find((x) => x.id === m.id)
        if (stored) stored.delivery = 'read'
        emit(threadId, { type: 'delivery', messageId: m.id, state: 'read' })
      }, 500)
      streamReply(threadId, t.capability.mode)
      return m
    },
    async markRead(threadId) {
      const t = threads.get(threadId)
      if (t && t.unread) {
        t.unread = 0
        emitThreads()
      }
    },
    subscribe(threadId, listener) {
      const set = subs.get(threadId) ?? new Set()
      set.add(listener)
      subs.set(threadId, set)
      const t = threads.get(threadId)
      if (t) listener({ type: 'capability', capability: t.capability })
      return () => void set.delete(listener)
    },
    subscribeThreads(listener) {
      listSubs.add(listener)
      return () => void listSubs.delete(listener)
    },
    inject(threadId, body) {
      const m: ChatMessage = { id: nid(), threadId, from: 'agent', body, at: now(), tools: [] }
      push(m, true)
      emit(threadId, { type: 'message', message: m })
      emitThreads()
    },
    sentBodies: () => [...sent],
  }
}
