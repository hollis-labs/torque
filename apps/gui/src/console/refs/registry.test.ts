import { describe, expect, it } from 'vitest'
import { createDefaultRefRegistry, createRefRegistry, detectRefs, buildPreviewCard } from './index'
import type { RefKind } from './types'

const forgePr: RefKind = {
  id: 'forge.pr',
  label: 'Pull request',
  icon: 'pr',
  priority: 75,
  scan(text) {
    const out = []
    for (const m of text.matchAll(/\b([\w.-]+\/[\w.-]+)#(\d+)\b/g)) {
      out.push({ start: m.index!, end: m.index! + m[0].length, canonical: `${m[1]}#${m[2]}` })
    }
    return out
  },
  open: (ref) => ({ type: 'external', href: `https://github.com/${ref.canonical.replace('#', '/pull/')}` }),
}

describe('registry', () => {
  it('registers, lists by priority, unregisters', () => {
    const r = createDefaultRefRegistry()
    const ids = r.list().map((k) => k.id)
    expect(ids[0]).toBe('url')
    expect(ids).toContain('torque.task')
    expect(r.unregister('path')).toBe(true)
    expect(r.has('path')).toBe(false)
  })
  it('rejects duplicate and malformed ids, allows replace', () => {
    const r = createDefaultRefRegistry()
    expect(() => r.register({ ...forgePr, id: 'url' })).toThrow(/already registered/)
    expect(() => r.register({ ...forgePr, id: 'Bad Id' })).toThrow(/invalid/)
    expect(() => r.register({ ...forgePr, priority: NaN })).toThrow(/priority/)
    expect(() => r.register({ ...forgePr, id: 'url' }, { replace: true })).not.toThrow()
  })
  it('bumps version on change and unregister fn removes only its own kind', () => {
    const r = createRefRegistry()
    const v0 = r.version
    const off = r.register(forgePr)
    expect(r.version).toBeGreaterThan(v0)
    const replacement = { ...forgePr }
    r.register(replacement, { replace: true })
    off() // stale handle must not remove the replacement
    expect(r.get('forge.pr')).toBe(replacement)
  })
  it('lets a later feature (G2) add a kind that participates in detection', () => {
    const r = createDefaultRefRegistry()
    r.register(forgePr)
    const hits = detectRefs('fixes hollis-labs/torque#265 and src/a.ts', r)
    expect(hits.map((h) => [h.kind, h.canonical])).toEqual([
      ['forge.pr', 'hollis-labs/torque#265'],
      ['path', 'src/a.ts'],
    ])
  })
  it('a higher-priority plugin kind outranks built-ins on the same span', () => {
    const r = createDefaultRefRegistry()
    r.register({ ...forgePr, id: 'forge.repo', priority: 95, scan: (t) => (t.includes('a/b.ts') ? [{ start: t.indexOf('a/b.ts'), end: t.indexOf('a/b.ts') + 6, canonical: 'a/b' }] : []) })
    expect(detectRefs('a/b.ts', r).map((h) => h.kind)).toEqual(['forge.repo'])
  })
})

describe('preview cards', () => {
  const r = createDefaultRefRegistry()
  const ref = { kind: 'torque.task', canonical: 'CW-20261016-0101' }
  it('covers every state', () => {
    expect(buildPreviewCard(r, ref, { status: 'idle' }).phase).toBe('loading')
    expect(buildPreviewCard(r, ref, { status: 'loading' }).phase).toBe('loading')
    const nf = buildPreviewCard(r, ref, { status: 'not-found', fetchedAt: 0 })
    expect(nf).toMatchObject({ phase: 'not-found', subtitle: 'Torque task not found', open: { type: 'route', to: '/tasks/CW-20261016-0101' } })
    const found = buildPreviewCard(r, ref, { status: 'found', fetchedAt: 0, entity: { title: 'T', status: { label: 'done', tone: 'success' } } })
    expect(found).toMatchObject({ phase: 'ready', title: 'T' })
    expect(buildPreviewCard(r, ref, { status: 'error', message: 'x', fetchedAt: 0 })).toMatchObject({ phase: 'error', error: 'x' })
    expect(buildPreviewCard(r, ref, { status: 'unsupported' }).phase).toBe('unresolved')
  })
  it('keeps stale data while reloading', () => {
    expect(buildPreviewCard(r, ref, { status: 'loading', stale: { title: 'Old' } }).title).toBe('Old')
  })
  it('degrades for an unregistered kind', () => {
    expect(buildPreviewCard(r, { kind: 'gone', canonical: 'x' }, { status: 'idle' })).toMatchObject({ kindLabel: 'gone', open: { type: 'none' } })
  })
})

describe('open targets', () => {
  const r = createDefaultRefRegistry()
  it('routes, links and copies', () => {
    expect(r.get('torque.epic')!.open({ kind: 'torque.epic', canonical: 'EP-1' })).toEqual({ type: 'route', to: '/epics/EP-1' })
    expect(r.get('url')!.open({ kind: 'url', canonical: 'https://e.com' })).toEqual({ type: 'external', href: 'https://e.com' })
    expect(r.get('path')!.open({ kind: 'path', canonical: 'a/b.ts' })).toEqual({ type: 'copy', text: 'a/b.ts' })
  })
  it('chip labels', () => {
    expect(r.get('path')!.chipLabel!({ kind: 'path', canonical: 'a/b.ts', meta: { line: 3 } }, '')).toBe('b.ts:3')
    expect(r.get('tesseract.item')!.chipLabel!({ kind: 'tesseract.item', canonical: '01M51F1RPQ5FD8MHP71TRSBN7H' }, '')).toBe('01M5…N7H'.replace('N7H', 'BN7H'))
    expect(r.get('url')!.chipLabel!({ kind: 'url', canonical: 'https://example.com/' }, '')).toBe('example.com')
  })
})
