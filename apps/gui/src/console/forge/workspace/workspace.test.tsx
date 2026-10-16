/** @vitest-environment jsdom */
import { afterEach, describe, expect, it } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { createActionBus, MockIdentityConfirm } from '../../seams'
import { createMockForgeClient, MOCK_REPO } from '../mock'
import { ForgeError, type DiffLine, type FileDiff, type ForgeComment, type PrRef } from '../types'
import type { ForgeClient } from '../client'
import { buildFileTree, buildThreadIndex, splitRows, tokenize, langOf, unifiedRows } from './diff-utils'
import { PrWorkspace, PrWorkspacePage, prRefFromParams, prWorkspacePath } from '.'

afterEach(cleanup)

const PR1: PrRef = { repo: MOCK_REPO, number: 271 }
const PR2: PrRef = { repo: MOCK_REPO, number: 268 }

const ln = (kind: DiffLine['kind'], text: string, n: number): DiffLine => ({ kind, text, ...(kind !== 'add' ? { oldLine: n } : {}), ...(kind !== 'del' ? { newLine: n } : {}) })

function bigFile(lines: number): FileDiff {
  return {
    path: 'big/huge.ts', status: 'modified', additions: lines, deletions: 0,
    hunks: [{ oldStart: 1, oldLines: 0, newStart: 1, newLines: lines, lines: Array.from({ length: lines }, (_, i) => ({ kind: 'add' as const, text: `const v${i} = ${i}`, newLine: i + 1 })) }],
  }
}

function setup(client: ForgeClient = createMockForgeClient(), ref: PrRef = PR1, mode: 'auto-grant' | 'manual' = 'manual') {
  const identity = new MockIdentityConfirm({ mode })
  const bus = createActionBus({ identity })
  const ui = render(<PrWorkspace prRef={ref} client={client} bus={bus} identity={identity} />)
  return { identity, bus, client, ...ui }
}

describe('diff utils', () => {
  it('pairs deletions with following additions in split rows', () => {
    const f: FileDiff = {
      path: 'a.ts', status: 'modified', additions: 2, deletions: 1,
      hunks: [{ oldStart: 1, oldLines: 2, newStart: 1, newLines: 3, lines: [ln('context', 'x', 1), ln('del', 'old', 2), ln('add', 'new', 2), ln('add', 'more', 3)] }],
    }
    const rows = splitRows(f).filter((r) => r.type === 'split')
    expect(rows).toHaveLength(3)
    expect(rows[1]).toMatchObject({ left: { text: 'old' }, right: { text: 'new' } })
    expect(rows[2]).toMatchObject({ left: undefined, right: { text: 'more' } })
    expect(unifiedRows(f)).toHaveLength(5)
  })

  it('tokenizes strings, comments, numbers and keywords', () => {
    const kinds = tokenize('const a = "x" // hi', langOf('a.ts')).map((t) => t.kind)
    expect(kinds).toEqual(['keyword', 'plain', 'string', 'plain', 'comment'])
    expect(tokenize('# note', langOf('x.py'))[0].kind).toBe('comment')
    expect(tokenize('anything', langOf('x.unknown'))).toEqual([{ text: 'anything', kind: 'plain' }])
  })

  it('classifies threads: anchored, outdated (detached), orphaned', () => {
    const f: FileDiff = { path: 'a.ts', status: 'modified', additions: 1, deletions: 0, hunks: [{ oldStart: 1, oldLines: 0, newStart: 1, newLines: 1, lines: [ln('add', 'x', 1)] }] }
    const u = { login: 'u' }
    const mk = (id: string, extra: object): ForgeComment => ({ kind: 'review', id, author: u, body: id, createdAt: id, path: 'a.ts', line: 1, side: 'right', ...extra }) as ForgeComment
    const idx = buildThreadIndex(
      [mk('1', { commitSha: 'head' }), mk('2', { inReplyTo: '1' }), mk('3', { commitSha: 'old' }), mk('4', { path: 'gone.ts' }), mk('5', { line: 99, commitSha: 'head' })],
      [f],
      'head',
    )
    expect(idx.byAnchor.get('a.ts:right:1')?.map((t) => [t.id, t.replies.length])).toEqual([['1', 1]])
    expect(idx.detached.get('a.ts')?.map((t) => [t.id, t.outdated])).toEqual([['3', true], ['5', false]])
    expect(idx.orphaned.map((t) => t.id)).toEqual(['4'])
  })

  it('builds a compacted tree', () => {
    const f = (path: string): FileDiff => ({ path, status: 'modified', additions: 0, deletions: 0, hunks: [] })
    const tree = buildFileTree([f('a/b/c.ts'), f('a/b/d.ts'), f('root.ts')])
    expect(tree[0]).toMatchObject({ name: 'a/b', dir: true })
    expect(tree[0].children.map((c) => c.name)).toEqual(['c.ts', 'd.ts'])
  })

  it('round-trips the route helper', () => {
    const path = prWorkspacePath(PR1, { file: 'a b.ts' })
    expect(path).toBe('/pr/github/hollis-labs/torque/271?file=a%20b.ts')
    expect(prRefFromParams({ host: 'github', owner: 'hollis-labs', name: 'torque', number: '271' })).toEqual(PR1)
    expect(prRefFromParams({ host: 'github', owner: 'o', name: 'n', number: 'x' })).toBeNull()
  })
})

describe('PrWorkspace', () => {
  it('renders header, files, existing threads and switches to split', async () => {
    setup()
    expect(await screen.findByText(/alert tray with server-backed/)).toBeTruthy()
    expect(screen.getByText('3/3 passed')).toBeTruthy()
    expect(screen.getByText('Changes requested', { selector: 'span.inline-flex' })).toBeTruthy()
    const files = screen.getAllByTestId('forge-file')
    expect(files).toHaveLength(2)
    const thread = screen.getByTestId('forge-thread')
    expect(within(thread).getByText(/dedicated button/)).toBeTruthy()
    expect(within(thread).getByText(/explicit dismiss button/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'split' }))
    expect(screen.getByTestId('forge-thread')).toBeTruthy()
  })

  it('handles binary, renamed and removed files, and draft/conflict state', async () => {
    setup(createMockForgeClient(), PR2)
    expect(await screen.findByText('Binary file not shown.')).toBeTruthy()
    expect(screen.getByText(/adapter\.old\.ts/)).toBeTruthy()
    expect(screen.getByText('Has merge conflicts')).toBeTruthy()
  })

  it('posts a general comment only after step-up, optimistically', async () => {
    const { identity, client } = setup()
    await screen.findByText(/alert tray/)
    fireEvent.click(screen.getByRole('tab', { name: /Conversation/ }))
    fireEvent.change(screen.getByLabelText('Add a comment…'), { target: { value: 'Looks good overall' } })
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }))
    await waitFor(() => expect(identity.pending()).toHaveLength(1))
    expect(identity.pending()[0].request.scope).toEqual(['forge.write'])
    // Nothing is posted and no placeholder is shown before the grant.
    expect(screen.queryByText('posting…')).toBeNull()
    expect((await client.listComments(PR1)).some((c) => c.body === 'Looks good overall')).toBe(false)
    await act(async () => identity.resolve(identity.pending()[0].id, 'granted'))
    expect(await screen.findByText('Looks good overall')).toBeTruthy()
    await waitFor(() => expect(screen.queryByText('posting…')).toBeNull())
    expect(screen.getByText('Looks good overall')).toBeTruthy()
  })

  it('posts nothing and keeps the draft when the comment step-up is declined', async () => {
    const { identity, client } = setup()
    await screen.findByText(/alert tray/)
    fireEvent.click(screen.getAllByLabelText('Comment on line 1')[0])
    fireEvent.change(screen.getByLabelText('Comment on line 1…'), { target: { value: 'declined' } })
    fireEvent.click(within(screen.getByTestId('forge-inline-composer')).getByRole('button', { name: 'Comment' }))
    await waitFor(() => expect(identity.pending()).toHaveLength(1))
    await act(async () => identity.resolve(identity.pending()[0].id, 'denied', 'no'))
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect((screen.getByLabelText('Comment on line 1…') as HTMLTextAreaElement).value).toBe('declined')
    expect(screen.queryByText('posting…')).toBeNull()
    expect((await client.listComments(PR1)).some((c) => c.body === 'declined')).toBe(false)
  })

  it('rolls back a failed inline comment and keeps the draft', async () => {
    const base = createMockForgeClient()
    const failing: ForgeClient = Object.assign(Object.create(base), {
      addComment: async () => {
        throw new ForgeError('unavailable', 'forge down')
      },
    })
    setup(failing, PR1, 'auto-grant')
    await screen.findByText(/alert tray/)
    fireEvent.click(screen.getAllByLabelText('Comment on line 1')[0])
    fireEvent.change(screen.getByLabelText('Comment on line 1…'), { target: { value: 'will fail' } })
    fireEvent.click(within(screen.getByTestId('forge-inline-composer')).getByRole('button', { name: 'Comment' }))
    expect(await screen.findByText(/forge down/)).toBeTruthy()
    expect((screen.getByLabelText('Comment on line 1…') as HTMLTextAreaElement).value).toBe('will fail')
    expect(screen.queryByText('posting…')).toBeNull()
  })

  it('queues an inline comment and submits a review behind step-up', async () => {
    const { identity, client } = setup()
    await screen.findByText(/alert tray/)
    fireEvent.click(screen.getAllByLabelText('Comment on line 2')[0])
    fireEvent.change(screen.getByLabelText('Comment on line 2…'), { target: { value: 'nit: naming' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add to review' }))
    expect(screen.getByTestId('forge-pending-comment')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: /Review \(1\)/ }))
    fireEvent.click(screen.getByLabelText(/Request changes/))
    fireEvent.click(screen.getByRole('button', { name: 'Submit review' }))
    // Nothing is posted until the identity prompt is confirmed.
    await waitFor(() => expect(identity.pending()).toHaveLength(1))
    expect(identity.pending()[0].request.what.title).toMatch(/Request changes on/)
    expect((await client.getPr(PR1)).reviews).toHaveLength(1)
    await act(async () => identity.resolve(identity.pending()[0].id, 'granted'))
    await waitFor(async () => expect((await client.getPr(PR1)).reviews).toHaveLength(2))
    const comments = await client.listComments(PR1)
    expect(comments.some((c) => c.body === 'nit: naming' && c.kind === 'review' && !!c.reviewId)).toBe(true)
    await waitFor(() => expect(screen.queryByTestId('forge-review-panel')).toBeNull())
  })

  it('does not submit when step-up is cancelled', async () => {
    const { identity, client } = setup()
    await screen.findByText(/alert tray/)
    fireEvent.click(screen.getByRole('button', { name: 'Review' }))
    fireEvent.click(screen.getByLabelText(/^Approve/))
    fireEvent.click(screen.getByRole('button', { name: 'Submit review' }))
    await waitFor(() => expect(identity.pending()).toHaveLength(1))
    await act(async () => identity.resolve(identity.pending()[0].id, 'cancelled'))
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect((await client.getPr(PR1)).reviews).toHaveLength(1)
  })

  it('collapses large diffs and renders them in chunks', async () => {
    const base = createMockForgeClient()
    const big: ForgeClient = Object.assign(Object.create(base), { getDiff: async () => [bigFile(1000)] })
    setup(big)
    await screen.findByText(/alert tray/)
    expect(screen.getByText(/Large diff \(1000 lines\)/)).toBeTruthy()
    expect(screen.queryByRole('table')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /Expand big\/huge\.ts/ }))
    expect(screen.getAllByRole('row')).toHaveLength(300)
    fireEvent.click(screen.getByRole('button', { name: /Show all/ }))
    expect(screen.getAllByRole('row')).toHaveLength(1000)
  })

  it('shows outdated threads detached from the line, collapsed', async () => {
    const base = createMockForgeClient()
    const old: ForgeClient = Object.assign(Object.create(base), {
      listComments: async (r: PrRef) => (await base.listComments(r)).map((c) => (c.kind === 'review' ? { ...c, commitSha: 'deadbeef' } : c)),
    })
    setup(old)
    await screen.findByText(/alert tray/)
    const detached = screen.getByTestId('forge-detached-threads')
    expect(within(detached).getByText('Outdated')).toBeTruthy()
    expect(within(detached).queryByText(/dedicated button/)).toBeNull()
    fireEvent.click(within(detached).getByRole('button', { name: /Show 2/ }))
    expect(within(detached).getByText(/dedicated button/)).toBeTruthy()
  })

  it('reports a missing PR', async () => {
    setup(createMockForgeClient(), { repo: MOCK_REPO, number: 99999 })
    expect(await screen.findByText('Pull request not found')).toBeTruthy()
  })

  it('is read only for merged PRs and when asked', async () => {
    const identity = new MockIdentityConfirm()
    render(<PrWorkspace prRef={PR1} client={createMockForgeClient()} bus={createActionBus({ identity })} identity={identity} readOnly />)
    await screen.findByText(/alert tray/)
    expect(screen.queryByRole('button', { name: /^Review/ })).toBeNull()
    expect(screen.queryAllByLabelText(/Comment on line/)).toHaveLength(0)
  })

  it('mounts at the route', async () => {
    const { setForgeClient } = await import('../refs/client')
    const restore = setForgeClient(createMockForgeClient())
    render(
      <MemoryRouter initialEntries={['/pr/github/hollis-labs/torque/271?file=apps/gui/src/App.tsx']}>
        <Routes>
          <Route path="/pr/:host/:owner/:name/:number" element={<PrWorkspacePage />} />
        </Routes>
      </MemoryRouter>,
    )
    expect(await screen.findByText(/alert tray/)).toBeTruthy()
    restore()
  })
})
