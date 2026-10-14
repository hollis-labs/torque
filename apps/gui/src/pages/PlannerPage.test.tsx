/** @vitest-environment jsdom */
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { ApiError } from '@/lib/api'
import type { PlannerBoard, PlannerBoardRow, PlannerItem, PlannerSchemaInfo } from '@/lib/types'

const { api, event } = vi.hoisted(() => ({
  api: {
    plannerDatabases: vi.fn(),
    plannerBoard: vi.fn(),
    plannerTaskTitles: vi.fn(),
    plannerWorkstreamTasks: vi.fn(),
    getTask: vi.fn(),
    listComments: vi.fn(),
    plannerList: vi.fn(),
    plannerGet: vi.fn(),
    plannerSchema: vi.fn(),
    plannerSearch: vi.fn(),
    plannerCreate: vi.fn(),
    plannerUpdate: vi.fn(),
    plannerComment: vi.fn(),
    plannerLinkAdd: vi.fn(),
    plannerLinkRemove: vi.fn(),
    plannerReorder: vi.fn(),
    plannerDecide: vi.fn(),
    plannerDefer: vi.fn(),
    plannerReopen: vi.fn(),
    plannerInboxPromote: vi.fn(),
    plannerInboxDismiss: vi.fn(),
    setSetting: vi.fn(),
  },
  event: { current: null as unknown },
}))
vi.mock('@/hooks/use-api', () => ({ useApi: () => api }))
vi.mock('@/hooks/use-sse', () => ({ useSSE: () => ({ lastEvent: event.current }) }))
import PlannerPage from './PlannerPage'

const LINK_KINDS = ['torque', 'tesseract', 'tether', 'pr', 'adr', 'url', 'file', 'commit', 'workstream']

function schema(db: string, required: string[], fields: PlannerSchemaInfo['fields']): PlannerSchemaInfo {
  const enums: Record<string, string[]> = {}
  for (const [k, f] of Object.entries(fields)) if (f.enum) enums[k] = f.enum
  return {
    db: db as PlannerSchemaInfo['db'],
    id: `portfolio/${db}@1`,
    description: '',
    required,
    fields: { id: { type: 'string' }, rev: { type: 'integer' }, links: { type: 'array', items: 'link' }, comments: { type: 'array', items: 'comment' }, ...fields },
    enums,
    link_kinds: LINK_KINDS,
  }
}

const SCHEMAS: Record<string, PlannerSchemaInfo> = {
  risks: schema('risks', ['id', 'title', 'kind', 'severity', 'status'], {
    title: { type: 'string' },
    tldr: { type: 'string' },
    kind: { enum: ['risk', 'gap'] },
    severity: { enum: ['low', 'medium', 'high'] },
    status: { enum: ['open', 'closed'] },
    description: { type: 'string' },
    tags: { type: 'array', items: 'string' },
    related_ids: { type: 'array', items: 'string' },
  }),
  decisions: schema('decisions', ['id', 'title', 'status'], {
    title: { type: 'string' },
    related_ids: { type: 'array', items: 'string' },
    status: { enum: ['needs-decision', 'decided', 'deferred'] },
    rationale: { type: 'string' },
  }),
  inbox: schema('inbox', ['id', 'title'], { title: { type: 'string' }, body: { type: 'string' }, kind: { enum: ['topic', 'idea'] }, status: { enum: ['new', 'promoted', 'dismissed'] } }),
  ideas: schema('ideas', ['id', 'title', 'kind', 'status'], { title: { type: 'string' } }),
  roadmap: schema('roadmap', ['id', 'title'], { title: { type: 'string' }, status: { enum: ['planned', 'in-progress'] }, tasks: { type: 'array', items: 'string' } }),
  workstreams: schema('workstreams', ['id', 'title'], { title: { type: 'string' }, status: { enum: ['active', 'planned'] } }),
  priorities: schema('priorities', ['id', 'title', 'rank', 'status'], { title: { type: 'string' }, rank: { type: 'integer' }, status: { enum: ['active', 'queued'] } }),
}

const risk = (id: string, over: Partial<PlannerItem> = {}): PlannerItem => ({ id, title: `Risk ${id}`, rev: 1, kind: 'risk', severity: 'low', status: 'open', ...over })
const counts = (n: Record<string, number> = {}) =>
  ['priorities', 'workstreams', 'roadmap', 'ideas', 'decisions', 'risks', 'inbox'].map((name) => ({ name, prefix: '', schema: '', updated: '', count: n[name] ?? 0 }))

const emptyBoard = (over: Partial<PlannerBoard> = {}): PlannerBoard => ({
  generated_at: '2026-10-11T12:00:00.000Z',
  recent_hours: 72,
  active_hours: 2,
  limit: 30,
  sections: { in_flight: [], landed: [], pre_flight: [], holds: [] },
  counts: { in_flight: 0, landed: 0, pre_flight: 0, holds: 0 },
  totals: { in_flight: 0, landed: 0, pre_flight: 0, holds: 0 },
  notices: [],
  ...over,
})

function Probe() {
  const loc = useLocation()
  return <div data-testid="path">{loc.pathname + loc.search}</div>
}
function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Probe />
      <Routes>
        <Route path="/planner" element={<PlannerPage />} />
        <Route path="/planner/:db" element={<PlannerPage />} />
        <Route path="/planner/:db/:id" element={<PlannerPage />} />
      </Routes>
    </MemoryRouter>,
  )
}
const path = () => screen.getByTestId('path').textContent

beforeEach(() => {
  api.plannerDatabases.mockResolvedValue(counts({ risks: 3, decisions: 2, inbox: 1 }))
  api.plannerSchema.mockImplementation((db: string) => Promise.resolve(SCHEMAS[db]))
  api.plannerList.mockResolvedValue([])
  api.plannerBoard.mockResolvedValue(emptyBoard())
  api.plannerTaskTitles.mockResolvedValue({})
  api.listComments.mockResolvedValue({ items: [], meta: { returned: 0, limit: 5, has_more: false, next_cursor: null } })
  api.plannerWorkstreamTasks.mockResolvedValue({ workstream_id: 'WS-x', torque_project_ids: [], torque_tag: '', counts: {}, total: 0, open: [], open_total: 0, note: 'This workstream names no Torque project or tag.' })
})
afterEach(() => {
  cleanup()
  vi.resetAllMocks()
  event.current = null
})

it('offers to turn the planner on when the feature is off, then loads it', async () => {
  api.plannerDatabases.mockRejectedValueOnce(new ApiError(404, 'feature "planner" is not enabled', undefined, { code: 'feature_disabled' }))
  api.plannerList.mockRejectedValueOnce(new ApiError(404, 'feature "planner" is not enabled', undefined, { code: 'feature_disabled' }))
  api.setSetting.mockResolvedValue(undefined)
  renderAt('/planner/risks')
  await screen.findByText('The planner is switched off')
  api.plannerList.mockResolvedValue([risk('RK-001')])
  fireEvent.click(screen.getByRole('button', { name: 'Turn on planner' }))
  await screen.findByText('Risk RK-001')
  expect(api.setSetting).toHaveBeenCalledWith('features.planner', 'true')
})

it('lands on the board, shows the database tabs with counts and switches database through the route', async () => {
  api.plannerList.mockResolvedValue([risk('RK-001')])
  renderAt('/planner')
  expect(path()).toBe('/planner')
  const tab = await screen.findByRole('tab', { name: /Risks/ })
  expect(tab.textContent).toContain('3')
  expect(screen.getByRole('tab', { name: 'Board' }).getAttribute('aria-selected')).toBe('true')
  expect(api.plannerBoard).toHaveBeenCalled()
  expect(api.plannerList).not.toHaveBeenCalled() // the board does not load a list
  fireEvent.click(tab)
  await screen.findByText('Risk RK-001')
  expect(path()).toBe('/planner/risks')
  expect(api.plannerList).toHaveBeenLastCalledWith('risks')
  expect(screen.getByRole('tab', { name: 'Board' }).getAttribute('aria-selected')).toBe('false')
  fireEvent.click(screen.getByRole('tab', { name: 'Board' }))
  await waitFor(() => expect(path()).toBe('/planner/board'))
})

it('hides settled items until asked, filters by status chip and by text', async () => {
  api.plannerList.mockResolvedValue([
    risk('RK-001', { title: 'Backups', status: 'open', tldr: 'no backup of the planner' }),
    risk('RK-002', { title: 'Old thing', status: 'closed' }),
    risk('RK-003', { title: 'Search is slow', status: 'open' }),
  ])
  renderAt('/planner/risks')
  await screen.findByText('Backups')
  expect(screen.queryByText('Old thing')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: 'Show 1 settled' }))
  await screen.findByText('Old thing')

  fireEvent.click(screen.getByRole('button', { name: /^closed/ }))
  expect(screen.queryByText('Backups')).toBeNull()
  expect(screen.getByText('Old thing')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: /^closed/ })) // toggles off

  fireEvent.change(screen.getByRole('textbox', { name: 'Filter Risks' }), { target: { value: 'slow' } })
  expect(screen.queryByText('Backups')).toBeNull()
  expect(screen.getByText('Search is slow')).toBeTruthy()
})

it('opens an item from the URL with its fields, Torque link and comments, and adds a comment', async () => {
  const item = risk('RK-001', {
    title: 'Backups', tldr: 'no backup of the planner', description: 'The DB is one file.', rev: 4,
    links: [{ kind: 'torque', ref: 'CW-20261011-0050', label: 'api task' }, { kind: 'url', ref: 'https://example.com/x' }],
    comments: [{ id: 'c-1', author: 'chrispian', text: 'first!', created: '2026-10-11T10:00:00.000Z' }],
  })
  api.plannerList.mockResolvedValue([item])
  api.plannerGet.mockResolvedValue(item)
  api.plannerComment.mockResolvedValue({ ...item, rev: 5, comments: [...item.comments!, { id: 'c-2', author: 'chrispian', text: 'second', created: '2026-10-11T11:00:00.000Z' }] })
  renderAt('/planner/risks/RK-001')

  const dialog = await screen.findByRole('dialog')
  expect(within(dialog).getByText('The DB is one file.')).toBeTruthy()
  expect(within(dialog).getByText('rev 4', { exact: false })).toBeTruthy()
  const torque = within(dialog).getByRole('link', { name: 'CW-20261011-0050' })
  expect(torque.getAttribute('href')).toBe('/tasks/CW-20261011-0050')
  expect(within(dialog).getByText('api task')).toBeTruthy() // the link's own label
  const external = within(dialog).getByRole('link', { name: /example.com/ })
  expect(external.getAttribute('href')).toBe('https://example.com/x')
  expect(external.getAttribute('rel')).toContain('noopener')
  expect(within(dialog).getByText('first!')).toBeTruthy()

  fireEvent.change(within(dialog).getByPlaceholderText('Add a comment…'), { target: { value: 'second' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Comment' }))
  await within(dialog).findByText('second')
  expect(api.plannerComment).toHaveBeenCalledWith('risks', 'RK-001', 'second')
})

it('closing the dialog returns to the list route', async () => {
  const item = risk('RK-001')
  api.plannerList.mockResolvedValue([item])
  api.plannerGet.mockResolvedValue(item)
  renderAt('/planner/risks/RK-001')
  await screen.findByRole('dialog')
  fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
  await waitFor(() => expect(path()).toBe('/planner/risks'))
})

it('edits an item with its rev, and sends only what changed', async () => {
  const item = risk('RK-001', { title: 'Backups', rev: 4, tags: ['a'] })
  api.plannerList.mockResolvedValue([item])
  api.plannerGet.mockResolvedValue(item)
  api.plannerUpdate.mockResolvedValue({ ...item, title: 'Backups!', rev: 5 })
  renderAt('/planner/risks/RK-001')
  const dialog = await screen.findByRole('dialog')
  fireEvent.click(await within(dialog).findByRole('button', { name: 'Edit' }))
  fireEvent.change(within(dialog).getByLabelText('Title *'), { target: { value: 'Backups!' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
  await within(dialog).findByText('rev 5', { exact: false })
  expect(api.plannerUpdate).toHaveBeenCalledWith('risks', 'RK-001', { title: 'Backups!' }, 4)
})

it('shows the latest version when an edit conflicts', async () => {
  const item = risk('RK-001', { title: 'Mine', rev: 4 })
  const current = risk('RK-001', { title: 'Theirs', rev: 5 })
  api.plannerList.mockResolvedValue([item])
  api.plannerGet.mockResolvedValue(item)
  api.plannerUpdate.mockRejectedValue(new ApiError(409, 'conflict: rev mismatch', undefined, { code: 'conflict', details: { current } }))
  renderAt('/planner/risks/RK-001')
  const dialog = await screen.findByRole('dialog')
  fireEvent.click(await within(dialog).findByRole('button', { name: 'Edit' }))
  fireEvent.change(within(dialog).getByLabelText('Title *'), { target: { value: 'Mine, edited' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
  await within(dialog).findByText('Theirs')
  expect(within(dialog).queryByRole('button', { name: 'Save' })).toBeNull()
})

it('adds a Torque link, refusing a malformed task id first', async () => {
  const item = risk('RK-001')
  api.plannerList.mockResolvedValue([item])
  api.plannerGet.mockResolvedValue(item)
  api.plannerLinkAdd.mockResolvedValue({ ...item, rev: 2, links: [{ kind: 'torque', ref: 'CW-20261011-0050' }] })
  renderAt('/planner/risks/RK-001')
  const dialog = await screen.findByRole('dialog')
  fireEvent.click(await within(dialog).findByRole('button', { name: /Add link/ }))
  fireEvent.change(within(dialog).getByLabelText('Link target'), { target: { value: 'task-1' } })
  await within(dialog).findByText(/looks like CW-/)
  expect((within(dialog).getByRole('button', { name: 'Add link' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.change(within(dialog).getByLabelText('Link target'), { target: { value: 'CW-20261011-0050' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Add link' }))
  await within(dialog).findByRole('link', { name: 'CW-20261011-0050' })
  expect(api.plannerLinkAdd).toHaveBeenCalledWith('risks', 'RK-001', { kind: 'torque', ref: 'CW-20261011-0050' })
})

it('records a decision with its comment, and shows what was chosen', async () => {
  const open: PlannerItem = {
    id: 'DEC-021', title: 'Pick a model', rev: 1, status: 'needs-decision',
    options: [{ id: 'a', label: 'Typed tables', pros: 'Queryable' }, { id: 'b', label: 'One blob table' }],
  }
  api.plannerList.mockResolvedValue([open])
  api.plannerGet.mockResolvedValue(open)
  api.plannerDecide.mockResolvedValue({ ...open, rev: 2, status: 'decided', selected_option: 'a', decision: 'Typed tables' })
  renderAt('/planner/decisions/DEC-021')
  const dialog = await screen.findByRole('dialog')
  fireEvent.change(within(dialog).getByLabelText('Decision comment'), { target: { value: 'queries matter' } })
  fireEvent.click(await within(dialog).findByRole('button', { name: 'Choose option a: Typed tables' }))
  await within(dialog).findByText('chosen')
  expect(api.plannerDecide).toHaveBeenCalledWith('DEC-021', 'a', 'queries matter')
  expect(within(dialog).getByRole('button', { name: 'Reopen' })).toBeTruthy()
  expect(within(dialog).queryByRole('button', { name: /Choose option/ })).toBeNull()
})

it('shows why a decision was deferred and lets it be reopened', async () => {
  const deferred: PlannerItem = { id: 'DEC-052', title: 'Inbox long-term', rev: 2, status: 'deferred', defer_note: 'Settle before 1.0', options: [{ id: 'a', label: 'Tangent' }] }
  api.plannerList.mockResolvedValue([deferred])
  api.plannerGet.mockResolvedValue(deferred)
  api.plannerReopen.mockResolvedValue({ ...deferred, rev: 3, status: 'needs-decision', defer_note: undefined })
  renderAt('/planner/decisions/DEC-052')
  const dialog = await screen.findByRole('dialog')
  await within(dialog).findByText('Settle before 1.0')
  expect(within(dialog).getByRole('button', { name: 'Choose option a: Tangent' })).toBeTruthy() // still decidable
  fireEvent.click(within(dialog).getByRole('button', { name: 'Reopen' }))
  await waitFor(() => expect(api.plannerReopen).toHaveBeenCalledWith('DEC-052'))
  await waitFor(() => expect(within(dialog).queryByRole('button', { name: 'Reopen' })).toBeNull())
  expect(within(dialog).queryByText('Settle before 1.0')).toBeNull()
})

it('promotes an inbox item and opens the new item', async () => {
  const inbox: PlannerItem = { id: 'IN-001', title: 'Look at JEV', rev: 1, status: 'new', kind: 'idea' }
  api.plannerList.mockResolvedValue([inbox])
  api.plannerGet.mockImplementation((_db: string, id: string) => Promise.resolve(id === 'IN-001' ? inbox : { id: 'ID-look-at-jev', title: 'Look at JEV', rev: 1, status: 'new', kind: 'idea' }))
  api.plannerInboxPromote.mockResolvedValue({ inbox: { ...inbox, status: 'promoted' }, item: { id: 'ID-look-at-jev', title: 'Look at JEV', rev: 1 } })
  renderAt('/planner/inbox/IN-001')
  const dialog = await screen.findByRole('dialog')
  fireEvent.click(await within(dialog).findByRole('button', { name: 'Promote' }))
  await waitFor(() => expect(path()).toBe('/planner/ideas/ID-look-at-jev'))
  expect(api.plannerInboxPromote).toHaveBeenCalledWith('IN-001', 'ideas')
})

it('dismisses an inbox item with a note', async () => {
  const inbox: PlannerItem = { id: 'IN-002', title: 'Noise', rev: 1, status: 'new' }
  api.plannerList.mockResolvedValue([inbox])
  api.plannerGet.mockResolvedValue(inbox)
  api.plannerInboxDismiss.mockResolvedValue({ ...inbox, rev: 2, status: 'dismissed' })
  renderAt('/planner/inbox/IN-002')
  const dialog = await screen.findByRole('dialog')
  fireEvent.change(await within(dialog).findByLabelText('Dismiss note'), { target: { value: 'duplicate' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Dismiss' }))
  await waitFor(() => expect(api.plannerInboxDismiss).toHaveBeenCalledWith('IN-002', 'duplicate'))
  await waitFor(() => expect(within(dialog).queryByRole('button', { name: 'Promote' })).toBeNull())
})

it('searches every database and opens a hit', async () => {
  api.plannerList.mockResolvedValue([])
  api.plannerSearch.mockResolvedValue([{ db: 'roadmap', id: 'RM-llm-gateway', title: 'LLM gateway', status: 'in-progress' }])
  renderAt('/planner/risks')
  await screen.findByRole('tab', { name: /Risks/ })
  fireEvent.change(screen.getByRole('textbox', { name: 'Search the planner' }), { target: { value: 'gateway' } })
  const hit = await screen.findByRole('button', { name: 'Open RM-llm-gateway: LLM gateway' })
  expect(api.plannerSearch.mock.calls[0][0]).toBe('gateway')
  fireEvent.click(hit)
  await waitFor(() => expect(path()).toBe('/planner/roadmap/RM-llm-gateway'))
})

it('reloads when the server reports a planner change', async () => {
  api.plannerList.mockResolvedValue([risk('RK-001')])
  const view = renderAt('/planner/risks')
  await screen.findByText('Risk RK-001')
  const before = api.plannerList.mock.calls.length
  api.plannerList.mockResolvedValue([risk('RK-001'), risk('RK-009', { title: 'Added elsewhere' })])
  event.current = { type: 'planner.changed', data: { db: 'risks', id: 'RK-009' } }
  view.rerender(
    <MemoryRouter initialEntries={['/planner/risks']}>
      <Probe />
      <Routes>
        <Route path="/planner/:db" element={<PlannerPage />} />
      </Routes>
    </MemoryRouter>,
  )
  await screen.findByText('Added elsewhere')
  expect(api.plannerList.mock.calls.length).toBeGreaterThan(before)
})

it('creates an item from the required fields', async () => {
  api.plannerList.mockResolvedValue([])
  api.plannerCreate.mockResolvedValue(risk('RK-001', { title: 'New one' }))
  api.plannerGet.mockResolvedValue(risk('RK-001', { title: 'New one' }))
  renderAt('/planner/risks')
  fireEvent.click(await screen.findByRole('button', { name: 'New risk' }))
  const dialog = await screen.findByRole('dialog')
  const create = await within(dialog).findByRole('button', { name: 'Create risk' })
  await waitFor(() => expect(within(dialog).getByLabelText('Title *')).toBeTruthy())
  expect((create as HTMLButtonElement).disabled).toBe(true) // no title yet
  fireEvent.change(within(dialog).getByLabelText('Title *'), { target: { value: 'New one' } })
  await waitFor(() => expect((within(dialog).getByRole('button', { name: 'Create risk' }) as HTMLButtonElement).disabled).toBe(false))
  fireEvent.click(within(dialog).getByRole('button', { name: 'Create risk' }))
  await waitFor(() => expect(api.plannerCreate).toHaveBeenCalledWith('risks', { title: 'New one', kind: 'risk', severity: 'low', status: 'open' }))
  await waitFor(() => expect(path()).toBe('/planner/risks/RK-001'))
})

// ---------- board ----------

const brow = (over: Partial<PlannerBoardRow>): PlannerBoardRow => ({ source: 'torque', id: 'CW-20261011-0001', title: 'A task', status: 'doing', updated: '2026-10-11T10:00:00.000Z', ...over })

const wsrow = (over: Partial<PlannerBoardRow>): PlannerBoardRow => ({ source: 'workstream', db: 'workstreams', id: 'WS-a', title: 'Agents as data', status: 'active', updated: '2026-10-11T10:00:00.000Z', ...over })

it('shows the curated board: tagged workstreams with owner, next step, blockers and task counts, and no task rows', async () => {
  api.plannerBoard.mockResolvedValue(
    emptyBoard({
      scope: 'curated',
      sections: {
        in_flight: [wsrow({ owner: 'lead', next_step: 'Wire the registry', task_counts: { doing: 2, todo: 5 }, blockers: ['needs keys'] })],
        pre_flight: [wsrow({ id: 'WS-b', title: 'Mesh teams', status: 'planned' })],
        landed: [],
        holds: [wsrow({ blockers: ['needs keys'] }), brow({ source: 'decision', db: 'decisions', id: 'DEC-052', title: 'Pick a registry', status: 'needs-decision' })],
      },
      counts: { in_flight: 1, landed: 0, pre_flight: 1, holds: 2 },
      totals: { in_flight: 1, landed: 0, pre_flight: 1, holds: 2 },
    }),
  )
  renderAt('/planner/board')
  const motion = await screen.findByRole('region', { name: 'In motion' })
  expect(api.plannerBoard).toHaveBeenCalledWith({ scope: 'curated' })
  expect(within(motion).getByRole('link', { name: 'ACTIVE WS-a: Agents as data' }).getAttribute('href')).toBe('/planner/board?open=workstreams/WS-a')
  expect(within(motion).getByText('Next: Wire the registry')).toBeTruthy()
  expect(within(motion).getByText('tasks: 2 doing, 5 todo')).toBeTruthy()
  expect(within(motion).getByText('Blocked: needs keys')).toBeTruthy()
  expect(within(screen.getByRole('region', { name: 'Next up' })).getByRole('link', { name: 'PLANNED WS-b: Mesh teams' })).toBeTruthy()
  const waiting = screen.getByRole('region', { name: 'Waiting on you' })
  expect(within(waiting).getByRole('link', { name: 'BLOCKED WS-a: Agents as data' })).toBeTruthy()
  expect(within(waiting).getByRole('link', { name: 'DECIDE DEC-052: Pick a registry' })).toBeTruthy()
  expect(screen.queryByRole('link', { name: /CW-/ })).toBeNull()
})

it('explains an empty curated board and switches to the Activity feed', async () => {
  api.plannerBoard.mockImplementation((opts?: { scope?: string }) =>
    Promise.resolve(opts?.scope === 'all' ? emptyBoard({ scope: 'all', sections: { in_flight: [brow({ id: 'CW-20261011-0001', title: 'Build the API' })], landed: [], pre_flight: [], holds: [] }, counts: { in_flight: 1, landed: 0, pre_flight: 0, holds: 0 }, totals: { in_flight: 1, landed: 0, pre_flight: 0, holds: 0 } }) : emptyBoard({ scope: 'curated', notices: ['No planner items are tagged board. Tag a workstream with board to show it here (34 workstreams could be tagged).'] })),
  )
  renderAt('/planner/board')
  expect((await screen.findByRole('status')).textContent).toContain('No planner items are tagged board')
  expect(screen.queryByRole('region', { name: 'In flight' })).toBeNull()
  fireEvent.click(screen.getByRole('tab', { name: 'Activity' }))
  await waitFor(() => expect(path()).toBe('/planner/activity'))
  await screen.findByRole('link', { name: 'IN FLIGHT CW-20261011-0001: Build the API' })
  expect(api.plannerBoard).toHaveBeenLastCalledWith({ scope: 'all' })
  expect(screen.getByRole('tab', { name: 'Activity' }).getAttribute('aria-selected')).toBe('true')
  fireEvent.click(screen.getByRole('tab', { name: 'Board' }))
  await waitFor(() => expect(path()).toBe('/planner/board'))
  await screen.findByRole('region', { name: 'In motion' })
})

it('shows the activity feed with a row per line, the right words, and links to tasks and items', async () => {
  api.plannerBoard.mockResolvedValue(
    emptyBoard({
      sections: {
        in_flight: [brow({ id: 'CW-20261011-0001', title: 'Build the API', workstream: 'planner' }), brow({ id: 'CW-20261011-0002', title: 'Awaiting a look', status: 'review' }), brow({ source: 'roadmap', db: 'roadmap', id: 'RM-gui', title: 'GUI platform', status: 'in-progress' })],
        landed: [brow({ id: 'CW-20261011-0003', title: 'Shipped it', status: 'done', updated: '2026-10-11T11:30:00.000Z' }), brow({ source: 'decision', db: 'decisions', id: 'DEC-021', title: 'Typed tables', status: 'decided' })],
        pre_flight: [brow({ id: 'CW-20261011-0004', title: 'Quiet one', status: 'doing' }), brow({ id: 'CW-20261011-0005', title: 'Next up', status: 'queued' })],
        holds: [brow({ source: 'decision', db: 'decisions', id: 'DEC-052', title: 'Inbox long-term', status: 'needs-decision' }), brow({ source: 'risk', db: 'risks', id: 'RK-007', title: 'No backups', status: 'open' }), brow({ source: 'inbox', db: 'inbox', id: 'IN-001', title: 'Look at JEV', status: 'new' })],
      },
      counts: { in_flight: 3, landed: 2, pre_flight: 2, holds: 3 },
      totals: { in_flight: 9, landed: 2, pre_flight: 2, holds: 3 },
      notices: ['Task comments unavailable; every doing task is shown as in flight.'],
    }),
  )
  renderAt('/planner/activity')
  const flight = await screen.findByRole('region', { name: 'In flight' })
  expect(within(flight).getByText('(+6 more)')).toBeTruthy()
  expect(within(flight).getByRole('link', { name: 'IN FLIGHT CW-20261011-0001: Build the API' }).getAttribute('href')).toBe('/planner/activity?task=CW-20261011-0001')
  expect(within(flight).getByRole('link', { name: 'ARRIVING CW-20261011-0002: Awaiting a look' })).toBeTruthy()
  expect(within(flight).getByRole('link', { name: 'IN FLIGHT RM-gui: GUI platform' }).getAttribute('href')).toBe('/planner/activity?open=roadmap/RM-gui')
  expect(within(flight).getByText('planner')).toBeTruthy() // workstream
  expect(within(flight).getAllByText('2h').length).toBeGreaterThan(0) // 10:00 against a 12:00 board

  const landed = screen.getByRole('region', { name: 'Recently landed' })
  expect(within(landed).getByRole('link', { name: 'LANDED CW-20261011-0003: Shipped it' })).toBeTruthy()
  expect(within(landed).getByRole('link', { name: 'LANDED DEC-021: Typed tables' }).getAttribute('href')).toBe('/planner/activity?open=decisions/DEC-021')

  const pre = screen.getByRole('region', { name: 'Pre-flight' })
  expect(within(pre).getByRole('link', { name: 'PREPARING CW-20261011-0004: Quiet one' })).toBeTruthy() // doing, but in pre-flight
  expect(within(pre).getByRole('link', { name: 'QUEUED CW-20261011-0005: Next up' })).toBeTruthy()

  const holds = screen.getByRole('region', { name: 'Holds' })
  expect(within(holds).getByRole('link', { name: 'DECIDE DEC-052: Inbox long-term' })).toBeTruthy()
  expect(within(holds).getByRole('link', { name: 'RISK RK-007: No backups' })).toBeTruthy()
  expect(within(holds).getByRole('link', { name: 'TRIAGE IN-001: Look at JEV' }).getAttribute('href')).toBe('/planner/activity?open=inbox/IN-001')

  expect(screen.getByRole('status').textContent).toContain('Task comments unavailable')
})

it('says so when a board section is empty', async () => {
  renderAt('/planner')
  const holds = await screen.findByRole('region', { name: 'Waiting on you' })
  expect(within(holds).getByText('Nothing is waiting on you.')).toBeTruthy()
  expect(within(screen.getByRole('region', { name: 'Next up' })).getByText('No board workstream is planned.')).toBeTruthy()
})

it('offers a retry when the board fails, and the New button adds to the inbox', async () => {
  api.plannerBoard.mockRejectedValueOnce(new Error('database is locked'))
  renderAt('/planner')
  await screen.findByText('database is locked')
  expect(screen.getByRole('button', { name: 'New inbox item' })).toBeTruthy()
  api.plannerBoard.mockResolvedValue(emptyBoard())
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  await screen.findByRole('region', { name: 'In motion' })
})

it('refreshes the board when a task changes', async () => {
  const view = renderAt('/planner/activity')
  await screen.findByRole('region', { name: 'In flight' })
  const before = api.plannerBoard.mock.calls.length
  api.plannerBoard.mockResolvedValue(emptyBoard({ sections: { in_flight: [brow({ id: 'CW-20261011-0009', title: 'Just started' })], landed: [], pre_flight: [], holds: [] }, counts: { in_flight: 1, landed: 0, pre_flight: 0, holds: 0 }, totals: { in_flight: 1, landed: 0, pre_flight: 0, holds: 0 } }))
  event.current = { type: 'task.transitioned', data: { id: 'CW-20261011-0009' } }
  view.rerender(
    <MemoryRouter initialEntries={['/planner/activity']}>
      <Probe />
      <Routes>
        <Route path="/planner/:db" element={<PlannerPage />} />
      </Routes>
    </MemoryRouter>,
  )
  await screen.findByText('Just started')
  expect(api.plannerBoard.mock.calls.length).toBeGreaterThan(before)
  expect(api.plannerList).not.toHaveBeenCalled() // a task event does not reload a list the board is not showing
})

// ---------- live Torque references ----------

it('shows the live title and status of a linked Torque task, and says when Torque has no such task', async () => {
  const item = risk('RK-001', { links: [{ kind: 'torque', ref: 'CW-20261011-0050', label: 'api' }, { kind: 'torque', ref: 'CW-20200101-9999' }] })
  api.plannerList.mockResolvedValue([item])
  api.plannerGet.mockResolvedValue(item)
  api.plannerTaskTitles.mockResolvedValue({
    'CW-20261011-0050': { id: 'CW-20261011-0050', title: 'Planner 4/7: API and MCP tools', status: 'done', priority: 2, updated_at: '2026-10-11T18:00:00.000Z' },
    'CW-20200101-9999': null,
  })
  renderAt('/planner/risks/RK-001')
  const dialog = await screen.findByRole('dialog')
  await within(dialog).findByText('Planner 4/7: API and MCP tools')
  expect(within(dialog).getByText('done')).toBeTruthy()
  expect(within(dialog).getByText('not found in Torque')).toBeTruthy()
  expect(api.plannerTaskTitles).toHaveBeenCalledTimes(1) // one lookup for every id the item points at
  expect(api.plannerTaskTitles.mock.calls[0][0]).toEqual(['CW-20200101-9999', 'CW-20261011-0050'])
})

it("shows a roadmap item's tasks with their live titles", async () => {
  const item: PlannerItem = { id: 'RM-gw', title: 'Gateway', rev: 1, status: 'planned', tasks: ['CW-20261001-0607', 'CW-20261001-0608'] }
  api.plannerList.mockResolvedValue([item])
  api.plannerGet.mockResolvedValue(item)
  api.plannerTaskTitles.mockResolvedValue({ 'CW-20261001-0607': { id: 'CW-20261001-0607', title: 'Gateway budgets', status: 'doing', priority: 1, updated_at: '' }, 'CW-20261001-0608': null })
  renderAt('/planner/roadmap/RM-gw')
  const dialog = await screen.findByRole('dialog')
  await within(dialog).findByText('Gateway budgets')
  expect(within(dialog).getByRole('link', { name: 'CW-20261001-0607' }).getAttribute('href')).toBe('/tasks/CW-20261001-0607')
})

it("lists the Torque tasks behind a workstream, with counts per status", async () => {
  const ws: PlannerItem = { id: 'WS-planner', title: 'Planner', rev: 1, status: 'active', torque_tag: 'ws-planner' }
  api.plannerList.mockResolvedValue([ws])
  api.plannerGet.mockResolvedValue(ws)
  api.plannerWorkstreamTasks.mockResolvedValue({
    workstream_id: 'WS-planner', torque_project_ids: ['PRJ-20260416-0001'], torque_tag: 'ws-planner',
    counts: { doing: 2, done: 5 }, total: 7, open_total: 3,
    open: [{ id: 'CW-20261011-0053', title: 'Board view', status: 'doing', priority: 3, updated_at: '' }, { id: 'CW-20261011-0054', title: 'Follow-ups', status: 'todo', priority: 3, updated_at: '' }],
  })
  renderAt('/planner/workstreams/WS-planner')
  const dialog = await screen.findByRole('dialog')
  const panel = await within(dialog).findByRole('region', { name: 'Torque tasks' })
  expect(within(panel).getByText(/Torque tasks \(7\)/)).toBeTruthy()
  expect(within(panel).getByText('PRJ-20260416-0001 · tag ws-planner')).toBeTruthy()
  expect(within(panel).getByRole('link', { name: 'CW-20261011-0053' }).getAttribute('href')).toBe('/tasks/CW-20261011-0053')
  expect(within(panel).getByText('Showing 2 of 3 open tasks.')).toBeTruthy()
  expect(api.plannerWorkstreamTasks).toHaveBeenCalledWith('WS-planner', expect.anything())
})

it('tells a workstream with no Torque scope how to give it one', async () => {
  const ws: PlannerItem = { id: 'WS-bare', title: 'Bare', rev: 1, status: 'planned' }
  api.plannerList.mockResolvedValue([ws])
  api.plannerGet.mockResolvedValue(ws)
  renderAt('/planner/workstreams/WS-bare')
  const dialog = await screen.findByRole('dialog')
  await within(dialog).findByText(/names no Torque project or tag/)
  expect(within(dialog).getByText(/Set torque_project_ids or torque_tag/)).toBeTruthy()
})

// ---------- the board opens items in modals over itself ----------

const oneRowBoard = (row: PlannerBoardRow, section: 'in_flight' | 'holds' = 'in_flight') =>
  emptyBoard({
    sections: { in_flight: section === 'in_flight' ? [row] : [], landed: [], pre_flight: [], holds: section === 'holds' ? [row] : [] },
    counts: { in_flight: section === 'in_flight' ? 1 : 0, landed: 0, pre_flight: 0, holds: section === 'holds' ? 1 : 0 },
    totals: { in_flight: section === 'in_flight' ? 1 : 0, landed: 0, pre_flight: 0, holds: section === 'holds' ? 1 : 0 },
  })

it('opens a planner item in a modal over the board and returns to the board on close', async () => {
  const dec: PlannerItem = { id: 'DEC-052', title: 'Inbox long-term', rev: 2, status: 'needs-decision', options: [{ id: 'a', label: 'Tangent' }] }
  api.plannerBoard.mockResolvedValue(oneRowBoard(brow({ source: 'decision', db: 'decisions', id: 'DEC-052', title: 'Inbox long-term', status: 'needs-decision' }), 'holds'))
  api.plannerGet.mockResolvedValue(dec)
  renderAt('/planner/board')
  fireEvent.click(await screen.findByRole('link', { name: 'DECIDE DEC-052: Inbox long-term' }))

  const dialog = await screen.findByRole('dialog')
  expect(within(dialog).getByRole('button', { name: 'Choose option a: Tangent' })).toBeTruthy()
  expect(path()).toBe('/planner/board?open=decisions/DEC-052')
  // An open modal hides what is behind it from assistive tech, so look past that: the board is still there.
  expect(screen.getByRole('region', { name: 'Waiting on you', hidden: true })).toBeTruthy()
  expect(screen.getByRole('tab', { name: 'Board', hidden: true }).getAttribute('aria-selected')).toBe('true')
  expect(api.plannerList).not.toHaveBeenCalled() // no list was loaded behind the modal

  fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
  await waitFor(() => expect(path()).toBe('/planner/board'))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

it('opens a Torque task in a quick-view modal over the board, with its latest comments and a link to the full page', async () => {
  api.plannerBoard.mockResolvedValue(oneRowBoard(brow({ id: 'CW-20261011-0050', title: 'Planner 4/7' })))
  api.getTask.mockResolvedValue({
    id: 'CW-20261011-0050', title: 'Planner 4/7: API and MCP tools', description: 'HTTP routes and the 18 MCP tools.', status: 'done', priority: 2,
    tags: [{ slug: 'planner', name: 'planner', description: '', color: 'zinc', created_at: '', updated_at: '' }],
  })
  api.listComments.mockResolvedValue({ items: [{ id: 1, entity_type: 'task', entity_id: 'CW-20261011-0050', author: 'claude', content: 'Merged: PR #249', created_at: '2026-10-11T18:04:27Z' }], meta: { returned: 1, limit: 5, has_more: false, next_cursor: null } })
  renderAt('/planner')
  fireEvent.click(await screen.findByRole('link', { name: 'IN FLIGHT CW-20261011-0050: Planner 4/7' }))

  const dialog = await screen.findByRole('dialog')
  await within(dialog).findByText('Planner 4/7: API and MCP tools')
  expect(within(dialog).getByText('HTTP routes and the 18 MCP tools.')).toBeTruthy()
  expect(within(dialog).getByText('Merged: PR #249')).toBeTruthy()
  expect(within(dialog).getByText('planner')).toBeTruthy()
  expect(within(dialog).getByRole('link', { name: /Open the full task/ }).getAttribute('href')).toBe('/tasks/CW-20261011-0050')
  expect(api.getTask).toHaveBeenCalledWith('CW-20261011-0050', expect.anything())
  expect(api.listComments.mock.calls[0].slice(0, 3)).toEqual(['task', 'CW-20261011-0050', { limit: 5, sort_by: 'created_at', sort_dir: 'desc' }])
  expect(path()).toBe('/planner/board?task=CW-20261011-0050')

  fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
  await waitFor(() => expect(path()).toBe('/planner/board'))
})

it('still shows a Torque task when its comments cannot be loaded, and says when the task cannot', async () => {
  api.plannerBoard.mockResolvedValue(oneRowBoard(brow({ id: 'CW-20261011-0050', title: 'Planner 4/7' })))
  api.getTask.mockResolvedValue({ id: 'CW-20261011-0050', title: 'The task', description: '', status: 'doing', priority: 3, tags: [] })
  api.listComments.mockRejectedValue(new Error('bad sort'))
  const view = renderAt('/planner/board?task=CW-20261011-0050')
  const dialog = await screen.findByRole('dialog')
  await within(dialog).findByText('The task')
  expect(within(dialog).getByText('No description.')).toBeTruthy()
  expect(within(dialog).queryByText('Latest comments')).toBeNull()
  view.unmount()
  cleanup()

  api.getTask.mockRejectedValue(new Error('task CW-20261011-0050 not found'))
  renderAt('/planner/board?task=CW-20261011-0050')
  await screen.findByText('task CW-20261011-0050 not found')
})

it('keeps a related item open on the board when one is followed from inside a board modal', async () => {
  const dec: PlannerItem = { id: 'DEC-052', title: 'Inbox long-term', rev: 2, status: 'decided', related_ids: ['RM-messaging-inbox-pipeline'] }
  const rm: PlannerItem = { id: 'RM-messaging-inbox-pipeline', title: 'Single inbox', rev: 1, status: 'planned' }
  api.plannerGet.mockImplementation((_db: string, id: string) => Promise.resolve(id === 'DEC-052' ? dec : rm))
  renderAt('/planner/board?open=decisions/DEC-052')
  const dialog = await screen.findByRole('dialog')
  const chip = await within(dialog).findByRole('button', { name: 'RM-messaging-inbox-pipeline' })
  fireEvent.click(chip)
  await waitFor(() => expect(path()).toBe('/planner/board?open=roadmap/RM-messaging-inbox-pipeline'))
  await within(await screen.findByRole('dialog')).findByText('Single inbox')
  expect(screen.getByRole('region', { name: 'In motion', hidden: true })).toBeTruthy() // still the board behind it
})
