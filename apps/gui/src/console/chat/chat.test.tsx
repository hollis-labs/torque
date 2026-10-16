/** @vitest-environment jsdom */
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AutolinkProvider } from '../autolink'
import { createMockMessagingClient, MOCK_RECIPIENTS } from '../messaging'
import { ChatPanel, ChatProvider, applyChatEvent, createMailChatClient, createMockChatClient, mergeMessages, useChatUnread, type ChatMessage } from '.'

beforeEach(() => {
  vi.useFakeTimers()
  Element.prototype.scrollTo ??= () => undefined
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

const msg = (o: Partial<ChatMessage> = {}): ChatMessage => ({ id: 'm1', threadId: 't', from: 'agent', body: '', at: 1, tools: [], ...o })
const flush = () => act(async () => void (await vi.advanceTimersByTimeAsync(0)))
const tick = (ms: number) => act(async () => void (await vi.advanceTimersByTimeAsync(ms)))

function setup(client = createMockChatClient(), agent = null as (typeof MOCK_RECIPIENTS)[number] | null) {
  return render(
    <MemoryRouter>
      <AutolinkProvider>
        <ChatProvider client={client}>
          <ChatPanel agent={agent} />
        </ChatProvider>
      </AutolinkProvider>
    </MemoryRouter>,
  )
}

it('reduces streaming events in order and tolerates unknown ids', () => {
  let s = applyChatEvent({ messages: [] }, { type: 'message', message: msg({ streaming: true }) })
  s = applyChatEvent(s, { type: 'delta', messageId: 'm1', text: 'Hel' })
  s = applyChatEvent(s, { type: 'delta', messageId: 'm1', text: 'lo' })
  s = applyChatEvent(s, { type: 'tool', messageId: 'm1', tool: { id: 'a', name: 'bash', status: 'running' } })
  s = applyChatEvent(s, { type: 'tool', messageId: 'm1', tool: { id: 'a', name: 'bash', status: 'ok' } })
  s = applyChatEvent(s, { type: 'delta', messageId: 'nope', text: 'x' })
  s = applyChatEvent(s, { type: 'end', messageId: 'm1' })
  expect(s.messages).toHaveLength(1)
  expect(s.messages[0]).toMatchObject({ body: 'Hello', streaming: false, tools: [{ id: 'a', status: 'ok' }] })
})

it('merges history pages by id, oldest first', () => {
  const out = mergeMessages([msg({ id: 'b', at: 2 })], [msg({ id: 'a', at: 1 }), msg({ id: 'b', at: 2, body: 'new' })])
  expect(out.map((m) => m.id)).toEqual(['a', 'b'])
  expect(out[1].body).toBe('new')
})

it('lists threads with capability and unread, then opens one and clears unread', async () => {
  setup()
  await flush()
  expect(screen.getByText('LIVE')).toBeTruthy()
  expect(screen.getByText('MAIL ONLY')).toBeTruthy()
  expect(screen.getByText('OFFLINE')).toBeTruthy()
  expect(screen.getByLabelText('1 unread')).toBeTruthy()
  fireEvent.click(screen.getByText('Team Worker'))
  await flush()
  expect(screen.getByText(/Starting after the current PR/)).toBeTruthy()
  fireEvent.click(screen.getByLabelText('Back to threads'))
  await flush()
  expect(screen.queryByLabelText('1 unread')).toBeNull()
})

it('streams a reply with a tool chip and linkifies refs', async () => {
  const client = createMockChatClient()
  setup(client, MOCK_RECIPIENTS[0])
  await flush()
  const box = screen.getByLabelText('Message')
  fireEvent.change(box, { target: { value: 'status of CW-20261016-0117?' } })
  fireEvent.click(screen.getByText('Send'))
  await flush()
  expect(client.sentBodies()).toEqual(['status of CW-20261016-0117?'])
  await tick(700)
  expect(document.querySelector('[data-streaming="true"]')).toBeTruthy()
  await tick(5000)
  expect(document.querySelector('[data-streaming="true"]')).toBeNull()
  expect(screen.getByLabelText('Tool calls')).toBeTruthy()
  expect(document.querySelectorAll('[data-ref-kind="torque.task"]').length).toBeGreaterThan(0)
  expect(document.querySelector('[data-state="read"]')).toBeTruthy()
})

it('pages older history', async () => {
  setup(createMockChatClient({ historyCount: 45 }), MOCK_RECIPIENTS[0])
  await flush()
  const before = document.querySelectorAll('[data-from]').length
  fireEvent.click(screen.getByText('Load older messages'))
  await flush()
  expect(document.querySelectorAll('[data-from]').length).toBeGreaterThan(before)
})

it('offline agents queue without a reply and say so', async () => {
  const client = createMockChatClient()
  setup(client, { address: 'msg://agent/agent-mux/agt_rc03', label: 'Release Captain', kind: 'agent', agentId: 'release-captain' })
  await flush()
  expect(screen.getByText('Agent is offline. This will queue.')).toBeTruthy()
  fireEvent.change(screen.getByLabelText('Message'), { target: { value: 'ping' } })
  fireEvent.click(screen.getByText('Send'))
  await flush()
  await tick(10_000)
  expect(document.querySelectorAll('[data-from="agent"]')).toHaveLength(0)
})

it('shows send errors and keeps the draft', async () => {
  const client = createMockChatClient()
  client.send = async () => {
    throw new Error('boom')
  }
  setup(client, MOCK_RECIPIENTS[0])
  await flush()
  fireEvent.change(screen.getByLabelText('Message'), { target: { value: 'hi' } })
  fireEvent.click(screen.getByText('Send'))
  await flush()
  expect(screen.getByRole('alert').textContent).toBe('boom')
  expect((screen.getByLabelText('Message') as HTMLTextAreaElement).value).toBe('hi')
})

it('mail client sends via MessagingClient and surfaces new agent mail', async () => {
  const messaging = createMockMessagingClient()
  const agent = MOCK_RECIPIENTS[0]
  const chat = createMailChatClient(messaging, { operator: 'msg://user/local/operator', agents: [agent], pollMs: 1000 })
  const events: string[] = []
  const off = chat.subscribe(agent.address, (e) => events.push(e.type))
  const m = await chat.send(agent.address, 'hello')
  expect(m.delivery).toBe('queued')
  expect((await chat.history(agent.address)).messages.map((x) => x.body)).toEqual(['hello'])
  await messaging.send({ to: { ...agent, address: 'msg://user/local/operator' }, from: agent.address, intent: 'message', body: 'back' })
  expect((await chat.listThreads())[0].capability.mode).toBe('mail-only')
  off()
  expect(events[0]).toBe('capability')
})

it('useChatUnread totals unread across threads', async () => {
  function Badge() {
    return <span data-testid="n">{useChatUnread()}</span>
  }
  render(<ChatProvider client={createMockChatClient()}><Badge /></ChatProvider>)
  await flush()
  expect(screen.getByTestId('n').textContent).toBe('1')
})
