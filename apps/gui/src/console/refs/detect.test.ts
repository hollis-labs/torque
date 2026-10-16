import { describe, expect, it } from 'vitest'
import { codeRanges, createDefaultRefRegistry, createRefRegistry, detect, detectRefs, uniqueRefs } from './index'
import type { RefKind } from './types'

const reg = createDefaultRefRegistry()
const refs = (text: string, o?: Parameters<typeof detectRefs>[2]) => detectRefs(text, reg, o).map((r) => [r.kind, r.canonical] as const)
const raws = (text: string) => detectRefs(text, reg).map((r) => r.raw)

describe('torque ids', () => {
  it('detects each entity type', () => {
    expect(refs('CW-20261016-0101 EP-20261016-0002 PRJ-20260416-0001 SP-20260101-0003 PL-20260202-0004')).toEqual([
      ['torque.task', 'CW-20261016-0101'],
      ['torque.epic', 'EP-20261016-0002'],
      ['torque.project', 'PRJ-20260416-0001'],
      ['torque.sprint', 'SP-20260101-0003'],
      ['torque.plan', 'PL-20260202-0004'],
    ])
  })
  it('strips surrounding punctuation', () => {
    expect(raws('(see CW-20261016-0101), and "EP-20261016-0002". Done: PRJ-20260416-0001!')).toEqual([
      'CW-20261016-0101', 'EP-20261016-0002', 'PRJ-20260416-0001',
    ])
  })
  it('rejects malformed, embedded and file-name uses', () => {
    expect(refs('CW-2026101-0101 CW-20261016-01011 XCW-20261016-0101 CW-20261016-0101-foo CW-20261016-0101.md cw-20261016-0101')).toEqual([])
    expect(refs('tasks/CW-20261016-0101')).toEqual([])
  })
  it('allows a sentence-ending period', () => {
    expect(raws('Fixed in CW-20261016-0101.')).toEqual(['CW-20261016-0101'])
  })
})

describe('urls', () => {
  it('drops trailing punctuation and unbalanced closers', () => {
    expect(raws('see https://example.com/a/b.')).toEqual(['https://example.com/a/b'])
    expect(raws('(https://example.com/a)')).toEqual(['https://example.com/a'])
    expect(raws('[docs](https://example.com/a_(b))')).toEqual(['https://example.com/a_(b)'])
    expect(raws('**https://example.com/x**')).toEqual(['https://example.com/x'])
    expect(raws('go to <https://example.com/x>, now')).toEqual(['https://example.com/x'])
  })
  it('keeps query and fragment', () => {
    expect(raws('https://example.com/a?b=1&c=2#top')).toEqual(['https://example.com/a?b=1&c=2#top'])
  })
  it('accepts localhost, ignores hostless schemes', () => {
    expect(raws('http://localhost:5178/x')).toEqual(['http://localhost:5178/x'])
    expect(refs('https://nohost http://')).toEqual([])
  })
  it('beats inner kinds: ids and paths inside a URL are not separate refs', () => {
    const text = 'https://torque.example.com/tasks/CW-20261016-0101 and https://github.com/o/r/blob/main/src/a.ts'
    expect(refs(text)).toEqual([
      ['url', 'https://torque.example.com/tasks/CW-20261016-0101'],
      ['url', 'https://github.com/o/r/blob/main/src/a.ts'],
    ])
  })
  it('a URL inside markdown link text still wins over a task id in its path', () => {
    expect(refs('[x](https://e.com/EP-20261016-0002)').map((r) => r[0])).toEqual(['url'])
  })
})

describe('tesseract', () => {
  it('detects ULID item ids', () => {
    expect(refs('item 01M51F1RPQ5FD8MHP71TRSBN7H here')).toEqual([['tesseract.item', '01M51F1RPQ5FD8MHP71TRSBN7H']])
    expect(refs('tesseract:01M51F1RPQ5FD8MHP71TRSBN7H')).toEqual([['tesseract.item', '01M51F1RPQ5FD8MHP71TRSBN7H']])
  })
  it('rejects look-alikes', () => {
    expect(refs('ABCDEFGHJKMNPQRSTVWXYZABCD')).toEqual([]) // no digit
    expect(refs('01M51F1RPQ5FD8MHP71TRSBN7HX')).toEqual([]) // 27 chars
    expect(refs('01M51F1RPQ5FD8MHP71TRSBN7')).toEqual([]) // 25 chars
    expect(refs('01m51f1rpq5fd8mhp71trsbn7h')).toEqual([]) // lowercase
    expect(refs('91M51F1RPQ5FD8MHP71TRSBN7H')).toEqual([]) // first char > 7
  })
  it('detects explicit and bare keys', () => {
    expect(refs('tesseract:project/torque/workspace/handoff#agent_console_gui.')).toEqual([
      ['tesseract.key', 'project/torque/workspace/handoff#agent_console_gui'],
    ])
    expect(refs('tesseract:workspace@project/torque#k1')).toEqual([['tesseract.key', 'workspace@project/torque#k1']])
    expect(refs('see project/torque/workspace/handoff#my_key, ok')).toEqual([
      ['tesseract.key', 'project/torque/workspace/handoff#my_key'],
    ])
  })
  it('does not treat C#, issue#12, or a bare anchor as a key', () => {
    expect(refs('C# issue#12 foo/bar#baz README.md#install')).toEqual([])
  })
})

describe('workstreams', () => {
  it('detects ws- tags and explicit names', () => {
    expect(refs('tagged ws-agent-console, and workstream:billing-v2.')).toEqual([
      ['workstream', 'ws-agent-console'],
      ['workstream', 'billing-v2'],
    ])
  })
  it('ignores ws- inside words and short or numeric tokens', () => {
    expect(refs('news-feed knows-it ws-1 ws-a')).toEqual([])
    expect(raws('ws-agent-console-')).toEqual(['ws-agent-console'])
  })
})

describe('agents', () => {
  it('detects mentions', () => {
    expect(refs('ask @claude-code or @codex-12.')).toEqual([['agent', 'claude-code'], ['agent', 'codex-12']])
    expect(refs('agent:planner-1 is up')).toEqual([['agent', 'planner-1']])
  })
  it('ignores emails, npm scopes, handles with a single letter', () => {
    expect(refs('me@example.com @hollis-labs/ui @a @')).toEqual([])
  })
})

describe('paths', () => {
  it('detects absolute, home and repo-relative paths with line numbers', () => {
    expect(refs('/srv/agentcow/worktrees/x and ~/dev/hollis-labs and apps/gui/src/main.tsx:42:7')).toEqual([
      ['path', '/srv/agentcow/worktrees/x'],
      ['path', '~/dev/hollis-labs'],
      ['path', 'apps/gui/src/main.tsx'],
    ])
    expect(detectRefs('at src/a/b.ts:42:7', reg)[0].meta).toMatchObject({ line: 42, col: 7 })
  })
  it('detects dotted relatives and trailing-slash dirs', () => {
    expect(refs('../x/y.go ./cmd/server/ and docs/')).toEqual([['path', '../x/y.go'], ['path', './cmd/server']])
  })
  it('drops trailing punctuation', () => {
    expect(raws('edit src/lib/api.ts, then (src/lib/types.ts).')).toEqual(['src/lib/api.ts', 'src/lib/types.ts'])
  })
  it('rejects prose that merely contains slashes', () => {
    expect(refs('and/or either/or 10/16/2026 TCP/IP w/o 24/7 Q3/Q4 v1.2/3')).toEqual([])
    expect(refs('/api/planner/torque-titles /operations')).toEqual([])
  })
  it('rejects bare domains and versions', () => {
    expect(refs('github.com/hollis-labs/torque example.com/a/b.go 1.2.3/4.5')).toEqual([])
  })
  it('does not detect pieces of a URL or email', () => {
    expect(refs('https://example.com/dir/file.txt')).toEqual([['url', 'https://example.com/dir/file.txt']])
  })
  it('a task id inside a file name stays part of the path', () => {
    expect(refs('docs/CW-20261016-0101.md')).toEqual([['path', 'docs/CW-20261016-0101.md']])
  })
})

describe('code spans', () => {
  it('skips fenced blocks by default', () => {
    const t = 'before CW-20261016-0101\n```ts\nCW-20261016-0002 and src/a.ts\n```\nafter EP-20261016-0002'
    expect(refs(t)).toEqual([['torque.task', 'CW-20261016-0101'], ['torque.epic', 'EP-20261016-0002']])
  })
  it('skips inline code', () => {
    expect(refs('use `CW-20261016-0101` not CW-20261016-0102')).toEqual([['torque.task', 'CW-20261016-0102']])
    expect(refs('use ``a ` CW-20261016-0101`` ok')).toEqual([])
  })
  it('an unterminated fence runs to the end', () => {
    expect(refs('x\n```\nCW-20261016-0101')).toEqual([])
  })
  it('a longer closing fence closes; a shorter does not', () => {
    expect(refs('````\n```\nCW-20261016-0101\n````\nEP-20261016-0002')).toEqual([['torque.epic', 'EP-20261016-0002']])
    expect(refs('~~~\nCW-20261016-0101\n~~~\nEP-20261016-0002').length).toBe(1)
  })
  it('a lone backtick is literal', () => {
    expect(refs('a ` CW-20261016-0101')).toEqual([['torque.task', 'CW-20261016-0101']])
  })
  it('a ref straddling a span edge is dropped', () => {
    expect(refs('CW-2026`1016-0101` x')).toEqual([])
  })
  it('detects inside code when asked', () => {
    expect(refs('`CW-20261016-0101`', { skipCode: false })).toEqual([['torque.task', 'CW-20261016-0101']])
  })
  it('codeRanges are sorted and disjoint', () => {
    const r = codeRanges('`a` ```\nx\n``` `b`')
    for (let i = 1; i < r.length; i++) expect(r[i][0]).toBeGreaterThanOrEqual(r[i - 1][1])
  })
})

describe('overlaps and priority', () => {
  const kind = (id: string, priority: number, word: string): RefKind => ({
    id, label: id, icon: 'x', priority,
    scan: (t) => {
      const out = []
      let i = t.indexOf(word)
      while (i !== -1) { out.push({ start: i, end: i + word.length, canonical: word }); i = t.indexOf(word, i + 1) }
      return out
    },
    open: () => ({ type: 'none' }),
  })
  it('higher priority wins a contested span', () => {
    const r = createRefRegistry()
    r.register(kind('a.low', 1, 'foo bar'))
    r.register(kind('a.high', 5, 'bar baz'))
    expect(detectRefs('foo bar baz', r).map((x) => x.kind)).toEqual(['a.high'])
  })
  it('equal priority prefers the longer match', () => {
    const r = createRefRegistry()
    r.register(kind('a.short', 1, 'bar'))
    r.register(kind('a.long', 1, 'foo bar'))
    expect(detectRefs('foo bar', r).map((x) => x.kind)).toEqual(['a.long'])
  })
  it('keeps adjacent non-overlapping matches', () => {
    const r = createRefRegistry()
    r.register(kind('a.x', 1, 'ab'))
    expect(detectRefs('abab', r).length).toBe(2)
  })
  it('a ref fully inside a lower-priority match loses to the higher priority', () => {
    const r = createRefRegistry()
    r.register(kind('outer', 1, 'foo bar baz'))
    r.register(kind('inner', 9, 'bar'))
    expect(detectRefs('foo bar baz', r).map((x) => x.kind)).toEqual(['inner'])
  })
})

describe('segments', () => {
  it('reassemble to the input exactly', () => {
    const text = 'Hi @claude-code: CW-20261016-0101 → see `x` https://e.com/a. ✓ src/a.ts:3\n```\nz\n```\n'
    const segs = detect(text, reg)
    expect(segs.map((s) => text.slice(s.start, s.end)).join('')).toBe(text)
    expect(segs.filter((s) => s.type === 'ref').map((s) => s.type === 'ref' && s.kind)).toEqual(['agent', 'torque.task', 'url', 'path'])
  })
  it('handles empty and ref-only text', () => {
    expect(detect('', reg)).toEqual([])
    expect(detect('CW-20261016-0101', reg).map((s) => s.type)).toEqual(['ref'])
  })
  it('handles multibyte text without shifting offsets', () => {
    const text = '日本語 😀 CW-20261016-0101 😀'
    const [r] = detectRefs(text, reg)
    expect(text.slice(r.start, r.end)).toBe('CW-20261016-0101')
  })
  it('uniqueRefs dedupes in first-seen order', () => {
    const segs = detect('CW-20261016-0101 EP-20261016-0002 CW-20261016-0101', reg)
    expect(uniqueRefs(segs).map((r) => r.canonical)).toEqual(['CW-20261016-0101', 'EP-20261016-0002'])
  })
  it('limits to requested kinds', () => {
    expect(refs('CW-20261016-0101 @a-b', { kinds: ['agent'] })).toEqual([['agent', 'a-b']])
  })
})

describe('robustness', () => {
  it('survives a throwing or malformed kind', () => {
    const r = createDefaultRefRegistry()
    r.register({ id: 'bad.throws', label: 'x', icon: 'x', priority: 1, scan: () => { throw new Error('boom') }, open: () => ({ type: 'none' }) })
    r.register({ id: 'bad.hits', label: 'x', icon: 'x', priority: 1, scan: () => [{ start: 5, end: 2, canonical: 'x' }, { start: -1, end: 3, canonical: 'x' }, { start: 0, end: 99, canonical: 'x' }], open: () => ({ type: 'none' }) })
    const errs: string[] = []
    expect(detectRefs('CW-20261016-0101', r, { onError: (k) => errs.push(k) }).length).toBe(1)
    expect(errs).toContain('bad.throws')
    expect(errs.filter((k) => k === 'bad.hits').length).toBe(3)
  })
  it('linear on pathological inputs', () => {
    const inputs = [
      'a/'.repeat(50_000),
      'https://' + 'a.'.repeat(50_000),
      '`'.repeat(50_000),
      '`a'.repeat(30_000),
      '@' + 'a-'.repeat(50_000),
      'ws-' + 'a-'.repeat(50_000),
      '/home/' + 'x/'.repeat(50_000) + ':',
      'tesseract:project/' + 'a/'.repeat(50_000) + '#',
      'CW-20261016-'.repeat(20_000),
      '```\n'.repeat(20_000),
      '(' .repeat(50_000) + 'https://e.com/' + ')'.repeat(50_000),
    ]
    for (const input of inputs) {
      const t0 = performance.now()
      detect(input, reg)
      expect(performance.now() - t0).toBeLessThan(2000)
    }
  })
})
