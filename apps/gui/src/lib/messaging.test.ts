import { describe, it, expect } from 'vitest'
import {
  backfillThreadKeys,
  correspondentKey,
  extraPayloadFields,
  messageBody,
  messageScope,
  messageStatus,
  messageSubject,
  parseUrn,
  shortUrn,
  threadKey,
} from './messaging'
import type { MessageEnvelope } from './types'

function env(overrides: Partial<MessageEnvelope> = {}): MessageEnvelope {
  return {
    id: 'm1',
    kind: 'notice',
    from: 'msg://agent/local/orchestrator',
    to: 'msg://user/local/operator',
    created_at: '2026-05-18T12:00:00Z',
    delivered_at: null,
    consumed_at: null,
    ...overrides,
  }
}

describe('parseUrn', () => {
  it('parses a 3-segment URN', () => {
    expect(parseUrn('msg://agent/local/orchestrator')).toEqual({
      kind: 'agent',
      authority: 'local',
      id: 'orchestrator',
      subId: undefined,
    })
  })
  it('parses a 4-segment URN with subid', () => {
    expect(parseUrn('msg://session/host/abc/turn-2')).toEqual({
      kind: 'session',
      authority: 'host',
      id: 'abc',
      subId: 'turn-2',
    })
  })
  it('rejects a non-URN string', () => {
    expect(parseUrn('not-a-urn')).toBeNull()
    expect(parseUrn('')).toBeNull()
  })
})

describe('shortUrn', () => {
  it('drops the msg://<kind>/ prefix', () => {
    expect(shortUrn('msg://agent/local/orchestrator')).toBe('local/orchestrator')
  })
  it('keeps the subid tail', () => {
    expect(shortUrn('msg://session/host/abc/turn-2')).toBe('host/abc/turn-2')
  })
  it('passes through an unparseable value', () => {
    expect(shortUrn('garbage')).toBe('garbage')
  })
})

describe('messageScope', () => {
  it('user when addressed to a user URN', () => {
    expect(messageScope(env({ to: 'msg://user/local/op' }))).toBe('user')
  })
  it('agent for anything else', () => {
    expect(messageScope(env({ to: 'msg://agent/local/x' }))).toBe('agent')
    expect(messageScope(env({ to: 'msg://service/local/y' }))).toBe('agent')
  })
})

describe('messageSubject / messageBody', () => {
  it('reads subject + body from an object payload', () => {
    const m = env({ payload: { subject: 'Hi', body: 'the body' } })
    expect(messageSubject(m)).toBe('Hi')
    expect(messageBody(m)).toBe('the body')
  })
  it('reads a JSON string payload', () => {
    const m = env({ payload: JSON.stringify({ title: 'T', text: 'X' }) })
    expect(messageSubject(m)).toBe('T')
    expect(messageBody(m)).toBe('X')
  })
  it('falls back to the first body line when no subject', () => {
    const m = env({ payload: { body: 'line one\nline two' } })
    expect(messageSubject(m)).toBe('line one')
  })
  it('treats a bare string payload as the body', () => {
    expect(messageBody(env({ payload: 'plain text' }))).toBe('plain text')
  })
  it('empty for a null payload', () => {
    expect(messageBody(env({ payload: undefined }))).toBe('')
  })
})

describe('extraPayloadFields', () => {
  it('excludes projected subject/body keys', () => {
    const m = env({ payload: { subject: 'S', body: 'B', task_id: 'T-1', urgency: 'high' } })
    expect(extraPayloadFields(m)).toEqual([
      ['task_id', 'T-1'],
      ['urgency', 'high'],
    ])
  })
  it('empty for a non-object payload', () => {
    expect(extraPayloadFields(env({ payload: 'x' }))).toEqual([])
  })
})

describe('messageStatus', () => {
  it('pending when neither delivered nor consumed', () => {
    expect(messageStatus(env())).toBe('pending')
  })
  it('delivered when delivered_at is set', () => {
    expect(messageStatus(env({ delivered_at: '2026-05-18T12:01:00Z' }))).toBe('delivered')
  })
  it('consumed wins over delivered', () => {
    expect(
      messageStatus(
        env({ delivered_at: '2026-05-18T12:01:00Z', consumed_at: '2026-05-18T12:02:00Z' }),
      ),
    ).toBe('consumed')
  })
})

describe('threadKey', () => {
  it('uses thread_id when present', () => {
    expect(threadKey(env({ thread_id: 'th-9' }))).toBe('th-9')
  })
  it('trims thread_id before using it', () => {
    expect(threadKey(env({ thread_id: ' th-9 ' }))).toBe('th-9')
  })
  it('falls back to the message id', () => {
    expect(threadKey(env({ id: 'm7' }))).toBe('m7')
  })
})

describe('backfillThreadKeys', () => {
  it('uses explicit thread ids and falls back to envelope ids', () => {
    expect(
      backfillThreadKeys([
        env({ id: 'm1', thread_id: 'T-1' }),
        env({ id: 'm2', thread_id: '' }),
        env({ id: 'm3' }),
      ]),
    ).toEqual(['T-1', 'm2', 'm3'])
  })

  it('deduplicates and caps keys in message order', () => {
    expect(
      backfillThreadKeys(
        [
          env({ id: 'm1', thread_id: 'T-1' }),
          env({ id: 'm2', thread_id: 'T-1' }),
          env({ id: 'm3', thread_id: '' }),
          env({ id: 'm4', thread_id: '' }),
        ],
        2,
      ),
    ).toEqual(['T-1', 'm3'])
  })
})

describe('correspondentKey', () => {
  const self = 'msg://user/local/operator'
  it('inbound message keys on the sender', () => {
    const m = env({ from: 'msg://agent/local/orchestrator', to: self })
    expect(correspondentKey(m, self)).toBe('msg://agent/local/orchestrator')
  })
  it('outbound message keys on the recipient', () => {
    const m = env({ from: self, to: 'msg://agent/local/orchestrator' })
    expect(correspondentKey(m, self)).toBe('msg://agent/local/orchestrator')
  })
  it('groups inbound and outbound to the same correspondent', () => {
    const other = 'msg://session/host/abc/turn-2'
    const inbound = env({ id: 'a', from: other, to: self })
    const outbound = env({ id: 'b', from: self, to: other })
    expect(correspondentKey(inbound, self)).toBe(correspondentKey(outbound, self))
  })
  it('ignores surrounding whitespace on self and the URNs', () => {
    const m = env({ from: ' msg://user/local/operator ', to: ' msg://agent/local/x ' })
    expect(correspondentKey(m, '  msg://user/local/operator  ')).toBe('msg://agent/local/x')
  })
})
