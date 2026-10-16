import type { ChatCapability, ChatEvent, ChatMessage } from './types'

export interface ChatThreadState {
  messages: ChatMessage[]
  capability?: ChatCapability
}

const byTime = (a: ChatMessage, b: ChatMessage) => a.at - b.at || a.id.localeCompare(b.id)

/** Merge messages by id (later wins), oldest first. Used for history pages and upserts. */
export function mergeMessages(prev: ChatMessage[], next: ChatMessage[]): ChatMessage[] {
  const map = new Map(prev.map((m) => [m.id, m]))
  for (const m of next) {
    const old = map.get(m.id)
    map.set(m.id, old ? { ...old, ...m, tools: m.tools.length ? m.tools : old.tools } : m)
  }
  return [...map.values()].sort(byTime)
}

const patch = (s: ChatThreadState, id: string, f: (m: ChatMessage) => ChatMessage): ChatThreadState => {
  const i = s.messages.findIndex((m) => m.id === id)
  if (i < 0) return s // unknown id: tolerate out-of-order frames
  const messages = s.messages.slice()
  messages[i] = f(messages[i])
  return { ...s, messages }
}

export function applyChatEvent(s: ChatThreadState, e: ChatEvent): ChatThreadState {
  switch (e.type) {
    case 'message':
      return { ...s, messages: mergeMessages(s.messages, [e.message]) }
    case 'delta':
      return patch(s, e.messageId, (m) => ({ ...m, body: m.body + e.text, streaming: true }))
    case 'tool':
      return patch(s, e.messageId, (m) => {
        const has = m.tools.some((t) => t.id === e.tool.id)
        return { ...m, tools: has ? m.tools.map((t) => (t.id === e.tool.id ? e.tool : t)) : [...m.tools, e.tool] }
      })
    case 'end':
      return patch(s, e.messageId, (m) => ({ ...m, streaming: false, ...(e.error ? { error: e.error } : {}) }))
    case 'delivery':
      return patch(s, e.messageId, (m) => ({ ...m, delivery: e.state }))
    case 'capability':
      return { ...s, capability: e.capability }
  }
}
