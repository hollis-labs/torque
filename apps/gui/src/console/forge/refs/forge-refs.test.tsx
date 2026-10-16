/** @vitest-environment jsdom */
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AutolinkProvider, Linkified } from '../../autolink'
import { createDefaultRefRegistry, detectRefs, createRefResolverClient } from '../../refs'
import { createMockForgeClient } from '../mock'
import { registerForgeRefs, setKnownRepos, parseForgeUrl, findCommit } from '.'

afterEach(cleanup)

let registry = createDefaultRefRegistry()
let reg: ReturnType<typeof registerForgeRefs>
beforeEach(() => {
  registry = createDefaultRefRegistry()
  reg = registerForgeRefs({ registry, client: createMockForgeClient() })
  return () => reg.unregister()
})

const kinds = (t: string) => detectRefs(t, registry).map((h) => [h.kind, h.canonical])

describe('detection', () => {
  it('links owner/repo#N and forge PR URLs, not the generic url kind', () => {
    expect(kinds('fixes hollis-labs/torque#271 and https://github.com/hollis-labs/torque/pull/268.')).toEqual([
      ['forge.pr', 'github:hollis-labs/torque#271'],
      ['forge.pr', 'github:hollis-labs/torque#268'],
    ])
  })
  it('handles commits by URL and owner/repo@sha, never a bare sha or number', () => {
    expect(kinds('see hollis-labs/torque@1a2b3c4d and https://github.com/hollis-labs/torque/commit/9F3C2A1B7D0E')).toEqual([
      ['forge.commit', 'github:hollis-labs/torque@1a2b3c4d'],
      ['forge.commit', 'github:hollis-labs/torque@9f3c2a1b7d0e'],
    ])
    expect(kinds('deadbeef and a/b@1234567')).toEqual([])
  })
  it('repo shorthand only for known repos; repo URLs always', () => {
    expect(kinds('in hollis-labs/torque, not foo/bar or and/or')).toEqual([['forge.repo', 'github:hollis-labs/torque']])
    expect(kinds('https://github.com/acme/widgets')).toEqual([['forge.repo', 'github:acme/widgets']])
    const restore = setKnownRepos([{ host: 'github', owner: 'acme', name: 'widgets' }])
    expect(kinds('acme/widgets and hollis-labs/torque')).toEqual([['forge.repo', 'github:acme/widgets']])
    restore()
  })
  it('avoids false positives', () => {
    expect(kinds('src/app.ts#12 path/to/file#3 v1.2/x#4 foo#12 #271').filter(([k]) => k.startsWith('forge'))).toEqual([])
    expect(kinds('https://github.com/acme/widgets/issues/3 https://example.com/a/b/pull/3')).toEqual([['url', 'https://github.com/acme/widgets/issues/3'], ['url', 'https://example.com/a/b/pull/3']])
  })
  it('is host neutral', () => {
    expect(parseForgeUrl('https://gitlab.com/g/p/-/merge_requests/7')).toEqual({ type: 'pr', canonical: 'gitlab:g/p#7' })
    expect(parseForgeUrl('https://codeberg.org/o/r/pulls/2')).toEqual({ type: 'pr', canonical: 'gitea:o/r#2' })
    expect(kinds('gitlab:g/p#7')).toEqual([['forge.pr', 'gitlab:g/p#7']])
  })
})

describe('resolvers', () => {
  const client = () => createRefResolverClient({ resolvers: reg.resolvers, batchWindowMs: 0 })
  it('resolves PR, commit and repo through the ForgeClient', async () => {
    const c = client()
    const sig = new AbortController().signal
    const pr = await reg.resolvers['forge.pr'].resolveMany(['github:hollis-labs/torque#271', 'github:hollis-labs/torque#9999'], { signal: sig })
    expect(pr['github:hollis-labs/torque#271']).toMatchObject({ status: 'found', entity: { subtitle: 'hollis-labs/torque#271', status: { label: 'open' } } })
    const e = (pr['github:hollis-labs/torque#271'] as { entity: { fields: { label: string }[] } }).entity
    expect(e.fields.map((f) => f.label)).toEqual(expect.arrayContaining(['Author', 'Checks', 'Review', 'Size']))
    expect(pr['github:hollis-labs/torque#9999']).toEqual({ status: 'not-found' })
    const co = await reg.resolvers['forge.commit'].resolveMany(['github:hollis-labs/torque@1a2b3c4d', 'github:hollis-labs/torque@0000000'], { signal: sig })
    expect(co['github:hollis-labs/torque@1a2b3c4d']).toMatchObject({ status: 'found', entity: { title: 'feat(console): add AlertTray skeleton' } })
    expect(co['github:hollis-labs/torque@0000000']).toEqual({ status: 'not-found' })
    const repo = await reg.resolvers['forge.repo'].resolveMany(['github:hollis-labs/torque', 'github:x/y'], { signal: sig })
    expect(repo['github:hollis-labs/torque']?.status).toBe('found')
    expect(repo['github:x/y']).toEqual({ status: 'not-found' })
    c.dispose()
    expect(await findCommit(createMockForgeClient(), { host: 'github', owner: 'hollis-labs', name: 'torque' }, 'ffffffff')).toBeUndefined()
  })
})

describe('chip, hovercard and drawer', () => {
  it('click opens a drawer with checks, reviews and an external link', async () => {
    render(
      <MemoryRouter>
        <AutolinkProvider registry={registry} resolvers={reg.resolvers}>
          <Linkified text="shipped in hollis-labs/torque#271" />
        </AutolinkProvider>
      </MemoryRouter>,
    )
    const chip = document.querySelector('[data-ref-kind="forge.pr"]') as HTMLElement
    expect(chip.textContent).toBe('hollis-labs/torque#271')
    await waitFor(() => expect(chip.getAttribute('data-ref-state')).toBe('found'))
    fireEvent.click(chip)
    await waitFor(() => expect(screen.getByTestId('forge-pr-detail')).toBeTruthy())
    const drawer = screen.getByTestId('ref-drawer')
    expect(drawer.textContent).toContain('Checks')
    expect(drawer.textContent).toContain('Reviews')
    const link = [...drawer.querySelectorAll('a')].find((a) => a.textContent?.includes('Open on GitHub'))
    expect(link?.getAttribute('href')).toBe('https://github.com/hollis-labs/torque/pull/271')
  })
})
