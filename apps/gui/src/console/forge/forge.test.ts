import { describe, expect, it } from 'vitest'
import { FORGE_OP_EFFECTS, ForgeError, MOCK_REPO, MockForgeClient, prKey, repoKey } from './index'

const ref = { repo: MOCK_REPO, number: 271 }

describe('MockForgeClient', () => {
  it('lists open PRs newest first and filters', async () => {
    const c = new MockForgeClient()
    const all = await c.listPrs()
    expect(all.items.length).toBeGreaterThanOrEqual(3)
    expect(all.items.every((p) => p.state !== 'merged')).toBe(true)
    const dates = all.items.map((p) => p.updatedAt)
    expect([...dates].sort().reverse()).toEqual(dates)
    const mine = await c.listPrs({ repo: MOCK_REPO, assignee: 'dana-reviewer' })
    expect(mine.items.map((p) => p.ref.number)).toEqual([271])
    expect((await c.listPrs({ state: 'merged' })).items).toHaveLength(1)
  })

  it('paginates with cursor', async () => {
    const c = new MockForgeClient()
    const p1 = await c.listPrs({ state: 'all', limit: 2 })
    expect(p1.items).toHaveLength(2)
    expect(p1.nextCursor).toBeTruthy()
    const p2 = await c.listPrs({ state: 'all', limit: 2, cursor: p1.nextCursor! })
    expect(p2.items.length).toBeGreaterThan(0)
  })

  it('returns diffs with consistent counts', async () => {
    const c = new MockForgeClient()
    const diff = await c.getDiff(ref)
    const pr = await c.getPr(ref)
    expect(diff.reduce((n, f) => n + f.additions, 0)).toBe(pr.additions)
    for (const f of diff) for (const h of f.hunks) {
      expect(h.lines.filter((l) => l.kind !== 'add').length).toBe(h.oldLines)
      expect(h.lines.filter((l) => l.kind !== 'del').length).toBe(h.newLines)
    }
  })

  it('adds issue and inline comments, rejecting bad input', async () => {
    const c = new MockForgeClient()
    const issue = await c.addComment(ref, { body: 'hello' })
    expect(issue.kind).toBe('issue')
    const inline = await c.addComment(ref, { body: 'nit', path: 'apps/gui/src/App.tsx', line: 3 })
    expect(inline.kind).toBe('review')
    await expect(c.addComment(ref, { body: ' ' })).rejects.toMatchObject({ code: 'invalid_args' })
    await expect(c.addComment(ref, { body: 'x', path: 'nope.ts' })).rejects.toBeInstanceOf(ForgeError)
    expect((await c.listComments(ref)).length).toBe((await c.getPr(ref)).commentCount)
  })

  it('reviews and assigns', async () => {
    const c = new MockForgeClient()
    const r = await c.reviewPr(ref, { verdict: 'approve', body: 'ok' })
    expect(r.state).toBe('approved')
    expect((await c.getPr(ref)).reviews.at(-1)?.id).toBe(r.id)
    await expect(c.reviewPr(ref, { verdict: 'request_changes' })).rejects.toMatchObject({ code: 'invalid_args' })
    const pr = await c.assign(ref, { assignees: ['agent-qa'] })
    expect(pr.assignees[0].isAgent).toBe(true)
    await expect(c.reviewPr({ repo: MOCK_REPO, number: 266 }, { verdict: 'approve' })).rejects.toMatchObject({ code: 'conflict' })
  })

  it('summarises pending PRs and returns not_found', async () => {
    const c = new MockForgeClient()
    const pending = await c.listPendingPrs()
    const p268 = pending.find((p) => p.ref.number === 268)!
    expect(p268.reasons).toEqual(expect.arrayContaining(['checks_failed', 'conflicting', 'unassigned']))
    await expect(c.getPr({ repo: MOCK_REPO, number: 1 })).rejects.toMatchObject({ code: 'not_found' })
    expect(prKey(ref)).toBe(`${repoKey(MOCK_REPO)}#271`)
  })

  it('does not leak mutation through returned objects', async () => {
    const c = new MockForgeClient()
    const a = await c.getPr(ref)
    a.title = 'changed'
    expect((await c.getPr(ref)).title).not.toBe('changed')
  })

  it('classifies every client method with an effect', () => {
    const c = new MockForgeClient() as unknown as Record<string, unknown>
    for (const k of Object.keys(FORGE_OP_EFFECTS)) expect(typeof c[k]).toBe('function')
    expect(FORGE_OP_EFFECTS.reviewPr).toBe('write')
    expect(FORGE_OP_EFFECTS.getPr).toBe('read')
  })
})
