import { describe, expect, it } from 'vitest'
import {
  BOARD_SECTIONS,
  PLANNER_DBS,
  boardAge,
  boardMore,
  boardRowRoute,
  parseBoardOpen,
  boardWord,
  buildNewItem,
  buildPatch,
  conflictCurrent,
  dbOfId,
  draftFrom,
  fieldSpecs,
  filterItems,
  isSettled,
  itemFacts,
  linkTarget,
  missingRequired,
  moveId,
  statusCounts,
  statusToneKey,
  validateLinkDraft,
  validationErrors,
} from '@/lib/planner'
import type { PlannerBoardRow, PlannerItem, PlannerSchemaInfo } from '@/lib/types'

const item = (over: Partial<PlannerItem> & { id: string }): PlannerItem => ({ title: over.id, rev: 1, ...over })

// What GET /planner/schema/risks returns, trimmed.
const risksSchema: PlannerSchemaInfo = {
  db: 'risks',
  id: 'portfolio/risks@1',
  description: '',
  required: ['id', 'title', 'kind', 'severity', 'status'],
  link_kinds: ['torque', 'url'],
  enums: { kind: ['risk', 'gap'], severity: ['low', 'medium', 'high'], status: ['open', 'closed'] },
  fields: {
    id: { type: 'string' },
    title: { type: 'string' },
    tldr: { type: 'string' },
    tags: { type: 'array', items: 'string' },
    links: { type: 'array', items: 'link' },
    comments: { type: 'array', items: 'comment' },
    kind: { enum: ['risk', 'gap'] },
    severity: { enum: ['low', 'medium', 'high'] },
    status: { enum: ['open', 'closed'] },
    description: { type: 'string' },
    owner: { type: 'string' },
    priority: { type: 'integer', minimum: 1, maximum: 5 },
    rev: { type: 'integer' },
    order: { type: 'number' },
    roadmap_ids: { type: 'array', items: 'string' },
    mvp: { type: 'boolean' },
  },
}

describe('database metadata', () => {
  it('lists the seven databases with the inbox last', () => {
    expect(PLANNER_DBS.map((d) => d.key)).toEqual(['priorities', 'workstreams', 'roadmap', 'ideas', 'decisions', 'risks', 'inbox'])
  })

  it('finds the database of an id by prefix, longest first', () => {
    expect(dbOfId('DEC-012')).toBe('decisions')
    expect(dbOfId('ID-cliproxy-review')).toBe('ideas')
    expect(dbOfId('IN-003')).toBe('inbox')
    expect(dbOfId('WS-torque')).toBe('workstreams')
    expect(dbOfId('CW-20261011-0050')).toBeNull()
  })
})

describe('status', () => {
  it('maps planner statuses onto the kit tones and defaults unknown ones to backlog', () => {
    expect(statusToneKey('needs-decision')).toBe('todo')
    expect(statusToneKey('in-progress')).toBe('doing')
    expect(statusToneKey('DECIDED')).toBe('done')
    expect(statusToneKey('deferred')).toBe('paused')
    expect(statusToneKey('dropped')).toBe('archived')
    expect(statusToneKey('something-new')).toBe('backlog')
    expect(statusToneKey(undefined)).toBe('backlog')
  })

  it('knows which statuses are settled', () => {
    expect(isSettled('decided')).toBe(true)
    expect(isSettled('closed')).toBe(true)
    expect(isSettled('needs-decision')).toBe(false)
    expect(isSettled('deferred')).toBe(false)
    expect(isSettled(undefined)).toBe(false)
  })

  it('counts statuses in first-seen order', () => {
    const items = [item({ id: 'a', status: 'open' }), item({ id: 'b', status: 'closed' }), item({ id: 'c', status: 'open' }), item({ id: 'd' })]
    expect(statusCounts(items)).toEqual([{ status: 'open', count: 2 }, { status: 'closed', count: 1 }])
  })
})

describe('filterItems', () => {
  const items = [
    item({ id: 'RK-1', title: 'No backups', tldr: 'Planner DB has no backup', status: 'open' }),
    item({ id: 'RK-2', title: 'Slow search', status: 'closed' }),
    item({ id: 'RK-3', title: 'Flaky tests', tldr: 'search tests flake', status: 'open' }),
  ]
  it('filters by status', () => {
    expect(filterItems(items, { status: 'open' }).map((i) => i.id)).toEqual(['RK-1', 'RK-3'])
  })
  it('requires every term, across id, title and tldr, ignoring case', () => {
    expect(filterItems(items, { query: 'SEARCH' }).map((i) => i.id)).toEqual(['RK-2', 'RK-3'])
    expect(filterItems(items, { query: 'search tests' }).map((i) => i.id)).toEqual(['RK-3'])
    expect(filterItems(items, { query: 'rk-1' }).map((i) => i.id)).toEqual(['RK-1'])
    expect(filterItems(items, { query: '  ' })).toHaveLength(3)
  })
  it('combines both', () => {
    expect(filterItems(items, { status: 'open', query: 'search' }).map((i) => i.id)).toEqual(['RK-3'])
  })
})

describe('itemFacts', () => {
  it('picks the per-database facts and skips empty ones', () => {
    expect(itemFacts('roadmap', item({ id: 'RM-a', area: 'gui', horizon: 'now', mvp: true }))).toEqual(['gui', 'now', 'MVP'])
    expect(itemFacts('roadmap', item({ id: 'RM-b', area: 'gui', mvp: false }))).toEqual(['gui'])
    expect(itemFacts('priorities', item({ id: 'PR-01', rank: 3 }))).toEqual(['#3'])
    expect(itemFacts('risks', item({ id: 'RK-1', kind: 'gap', severity: 'high' }))).toEqual(['gap', 'high'])
    expect(itemFacts('inbox', item({ id: 'IN-1' }))).toEqual([])
  })
})

describe('moveId', () => {
  it('moves an id to the position of another', () => {
    expect(moveId(['a', 'b', 'c', 'd'], 'a', 'c')).toEqual(['b', 'c', 'a', 'd'])
    expect(moveId(['a', 'b', 'c', 'd'], 'd', 'b')).toEqual(['a', 'd', 'b', 'c'])
  })
  it('returns the same list when nothing moves or an id is unknown', () => {
    const ids = ['a', 'b']
    expect(moveId(ids, 'a', 'a')).toBe(ids)
    expect(moveId(ids, 'a', 'zzz')).toBe(ids)
  })
})

describe('links', () => {
  it('routes Torque tasks in-app, opens http(s) URLs outside, and leaves the rest as text', () => {
    expect(linkTarget({ kind: 'torque', ref: 'CW-20261011-0050' })).toEqual({ to: '/tasks/CW-20261011-0050', href: null })
    expect(linkTarget({ kind: 'url', ref: 'https://example.com/x' })).toEqual({ to: null, href: 'https://example.com/x' })
    expect(linkTarget({ kind: 'url', ref: 'javascript:alert(1)' })).toEqual({ to: null, href: null })
    expect(linkTarget({ kind: 'file', ref: '/tmp/x' })).toEqual({ to: null, href: null })
    expect(linkTarget({ kind: 'pr', ref: 'hollis-labs/torque#246' })).toEqual({ to: null, href: null })
  })

  it('validates a link draft the way the server does', () => {
    expect(validateLinkDraft('pr', 'x#1')).toBeNull()
    expect(validateLinkDraft('pr', '  ')).toMatch(/Enter/)
    expect(validateLinkDraft('url', 'ftp://x')).toMatch(/http/)
    expect(validateLinkDraft('url', 'https://x.test')).toBeNull()
    expect(validateLinkDraft('torque', 'task-1')).toMatch(/CW-/)
    expect(validateLinkDraft('torque', 'CW-20261003-0137')).toBeNull()
  })
})

describe('fieldSpecs', () => {
  const specs = fieldSpecs(risksSchema)
  const byName = Object.fromEntries(specs.map((s) => [s.name, s]))

  it('offers editable fields, title first, and leaves structural fields and object arrays to their own panels', () => {
    expect(specs[0].name).toBe('title')
    for (const hidden of ['id', 'rev', 'order', 'links', 'comments']) expect(byName[hidden]).toBeUndefined()
    expect(Object.keys(byName)).toEqual(expect.arrayContaining(['title', 'tldr', 'tags', 'kind', 'severity', 'status', 'description', 'owner', 'priority', 'roadmap_ids', 'mvp']))
  })

  it('picks the input kind from the schema', () => {
    expect(byName.title.kind).toBe('text')
    expect(byName.tldr.kind).toBe('longtext')
    expect(byName.description.kind).toBe('longtext')
    expect(byName.severity).toMatchObject({ kind: 'enum', options: ['low', 'medium', 'high'], required: true })
    expect(byName.priority).toMatchObject({ kind: 'int', min: 1, max: 5 })
    expect(byName.tags.kind).toBe('list')
    expect(byName.mvp.kind).toBe('bool')
    expect(byName.owner.required).toBe(false)
  })

  it('labels fields readably', () => {
    expect(byName.roadmap_ids.label).toBe('Roadmap links')
    expect(byName.tldr.label).toBe('Tldr')
  })
})

describe('drafts and patches', () => {
  const specs = fieldSpecs(risksSchema)
  const original = item({
    id: 'RK-1', title: 'Backups', kind: 'gap', severity: 'high', status: 'open',
    tldr: 'old', tags: ['a', 'b'], priority: 2, owner: 'pm',
  })

  it('round-trips an item through a draft without producing a patch', () => {
    expect(buildPatch(specs, original, draftFrom(specs, original))).toEqual({})
  })

  it('patches only what changed', () => {
    const d = draftFrom(specs, original)
    d.severity = 'low'
    d.tags = 'a\nc\n'
    d.priority = '4'
    expect(buildPatch(specs, original, d)).toEqual({ severity: 'low', tags: ['a', 'c'], priority: 4 })
  })

  it('clears a field with null, and does not null a field that was already empty', () => {
    const d = draftFrom(specs, original)
    d.owner = '   '
    d.tldr = ''
    d.description = ''
    expect(buildPatch(specs, original, d)).toEqual({ owner: null, tldr: null })
  })

  it('treats an unticked bool on an item without the field as unchanged, and a ticked one as true', () => {
    const d = draftFrom(specs, original)
    expect(d.mvp).toBe('false')
    expect(buildPatch(specs, original, d)).toEqual({})
    d.mvp = 'true'
    expect(buildPatch(specs, original, d)).toEqual({ mvp: true })
    expect(buildPatch(specs, { ...original, mvp: true }, { ...d, mvp: 'false' })).toEqual({ mvp: null })
  })

  it('ignores a non-numeric integer entry rather than sending junk', () => {
    const d = draftFrom(specs, original)
    d.priority = 'abc'
    expect(buildPatch(specs, original, d)).toEqual({ priority: null })
  })

  it('builds a new item from filled fields only and reports missing required ones', () => {
    const d = draftFrom(specs, null)
    expect(missingRequired(specs, d)).toEqual(['Title', 'Kind', 'Severity', 'Status'])
    Object.assign(d, { title: ' New risk ', kind: 'risk', severity: 'low', status: 'open', tags: 'x' })
    expect(missingRequired(specs, d)).toEqual([])
    expect(buildNewItem(specs, d)).toEqual({ title: 'New risk', kind: 'risk', severity: 'low', status: 'open', tags: ['x'] })
  })
})

describe('error details', () => {
  it('reads validation errors and the current item of a conflict', () => {
    expect(validationErrors({ errors: [{ path: 'severity', message: 'must be one of low, medium, high' }, { nope: 1 }] })).toEqual([
      { path: 'severity', message: 'must be one of low, medium, high' },
    ])
    expect(validationErrors(undefined)).toEqual([])
    expect(conflictCurrent({ current: { id: 'RK-1', title: 't', rev: 3 } })?.rev).toBe(3)
    expect(conflictCurrent({ current: 'x' })).toBeNull()
    expect(conflictCurrent(undefined)).toBeNull()
  })
})

describe('board', () => {
  const row = (over: Partial<PlannerBoardRow>): PlannerBoardRow => ({ source: 'torque', id: 'CW-1', title: 't', status: 'doing', updated: '2026-10-11T10:00:00.000Z', ...over })

  it('has the four sections in display order', () => {
    expect(BOARD_SECTIONS.map((s) => s.key)).toEqual(['in_flight', 'landed', 'pre_flight', 'holds'])
  })

  it('names rows the way the departure board does', () => {
    expect(boardWord('in_flight', row({ status: 'doing' }))).toEqual({ word: 'IN FLIGHT', tone: 'doing' })
    expect(boardWord('in_flight', row({ status: 'review' })).word).toBe('ARRIVING')
    expect(boardWord('in_flight', row({ source: 'roadmap', status: 'in-progress' })).word).toBe('IN FLIGHT')
    expect(boardWord('landed', row({ status: 'done' })).word).toBe('LANDED')
    expect(boardWord('landed', row({ source: 'decision', status: 'decided' })).word).toBe('LANDED')
    expect(boardWord('pre_flight', row({ status: 'queued' })).word).toBe('QUEUED')
    expect(boardWord('pre_flight', row({ source: 'roadmap', status: 'planned' })).word).toBe('QUEUED')
    expect(boardWord('pre_flight', row({ source: 'roadmap', status: 'adopting' })).word).toBe('BOARDING')
  })

  it('calls a doing task in pre-flight preparing, not in flight', () => {
    expect(boardWord('pre_flight', row({ status: 'doing' }))).toEqual({ word: 'PREPARING', tone: 'paused' })
  })

  it('names holds by what the owner has to do', () => {
    expect(boardWord('holds', row({ source: 'decision', status: 'needs-decision' })).word).toBe('DECIDE')
    expect(boardWord('holds', row({ source: 'inbox', status: 'new' })).word).toBe('TRIAGE')
    expect(boardWord('holds', row({ source: 'risk', status: 'open' }))).toEqual({ word: 'RISK', tone: 'blocked' })
  })

  it('falls back to the upper-cased status for one it does not know', () => {
    expect(boardWord('in_flight', row({ status: 'weird' })).word).toBe('WEIRD')
  })

  it('keeps the board underneath: a row opens its task or item in a modal over the board', () => {
    expect(boardRowRoute(row({ id: 'CW-20261011-0050' }))).toBe('/planner/board?task=CW-20261011-0050')
    expect(boardRowRoute(row({ source: 'decision', db: 'decisions', id: 'DEC-021' }))).toBe('/planner/board?open=decisions/DEC-021')
    expect(boardRowRoute(row({ source: 'roadmap', db: 'roadmap', id: 'RM-a b' }))).toBe('/planner/board?open=roadmap/RM-a%20b')
  })

  it('reads what the board is asked to open, and ignores anything malformed', () => {
    const q = (s: string) => parseBoardOpen(new URLSearchParams(s))
    expect(q('open=decisions/DEC-021')).toEqual({ item: { db: 'decisions', id: 'DEC-021' }, task: undefined })
    expect(q('open=roadmap/RM-a%20b').item).toEqual({ db: 'roadmap', id: 'RM-a b' })
    expect(q('task=CW-20261011-0050')).toEqual({ item: undefined, task: 'CW-20261011-0050' })
    expect(q('open=nope/X-1').item).toBeUndefined()
    expect(q('open=decisions').item).toBeUndefined()
    expect(q('open=decisions/').item).toBeUndefined()
    expect(q('')).toEqual({ item: undefined, task: undefined })
  })

  it('rounds ages as the text board does', () => {
    const now = Date.parse('2026-10-11T12:00:00.000Z')
    expect(boardAge('2026-10-11T11:55:00.000Z', now)).toBe('5m')
    expect(boardAge('2026-10-11T10:00:00.000Z', now)).toBe('2h')
    expect(boardAge('2026-10-10T12:00:00.000Z', now)).toBe('24h')
    expect(boardAge('2026-10-08T12:00:00.000Z', now)).toBe('3d')
    expect(boardAge('2026-10-11T13:00:00.000Z', now)).toBe('0m') // never negative
    expect(boardAge('', now)).toBe('')
    expect(boardAge('garbage', now)).toBe('')
  })

  it('says how many rows a section is not showing', () => {
    expect(boardMore(3, 9)).toBe('+6 more')
    expect(boardMore(3, 3)).toBe('')
    expect(boardMore(3, 2)).toBe('')
  })
})
