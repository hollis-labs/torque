/** @vitest-environment jsdom */
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { ApiError } from '@/lib/api'
import type { PlannerItem, PlannerSchemaInfo } from '@/lib/types'

const { api, event } = vi.hoisted(() => ({
  api: {
    plannerDatabases: vi.fn(),
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
    status: { enum: ['needs-decision', 'decided', 'deferred'] },
    rationale: { type: 'string' },
  }),
  inbox: schema('inbox', ['id', 'title'], { title: { type: 'string' }, body: { type: 'string' }, kind: { enum: ['topic', 'idea'] }, status: { enum: ['new', 'promoted', 'dismissed'] } }),
  ideas: schema('ideas', ['id', 'title', 'kind', 'status'], { title: { type: 'string' } }),
  priorities: schema('priorities', ['id', 'title', 'rank', 'status'], { title: { type: 'string' }, rank: { type: 'integer' }, status: { enum: ['active', 'queued'] } }),
}

const risk = (id: string, over: Partial<PlannerItem> = {}): PlannerItem => ({ id, title: `Risk ${id}`, rev: 1, kind: 'risk', severity: 'low', status: 'open', ...over })
const counts = (n: Record<string, number> = {}) =>
  ['priorities', 'workstreams', 'roadmap', 'ideas', 'decisions', 'risks', 'inbox'].map((name) => ({ name, prefix: '', schema: '', updated: '', count: n[name] ?? 0 }))

function Probe() {
  return <div data-testid="path">{useLocation().pathname}</div>
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

it('shows the database tabs with counts and switches database through the route', async () => {
  api.plannerList.mockResolvedValue([risk('RK-001')])
  renderAt('/planner')
  expect(path()).toBe('/planner')
  const tab = await screen.findByRole('tab', { name: /Risks/ })
  expect(tab.textContent).toContain('3')
  expect(screen.getByRole('tab', { name: /Priorities/ }).getAttribute('aria-selected')).toBe('true')
  expect(api.plannerList).toHaveBeenCalledWith('priorities')
  fireEvent.click(tab)
  await screen.findByText('Risk RK-001')
  expect(path()).toBe('/planner/risks')
  expect(api.plannerList).toHaveBeenLastCalledWith('risks')
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
  const torque = within(dialog).getByRole('link', { name: /api task \(CW-20261011-0050\)/ })
  expect(torque.getAttribute('href')).toBe('/tasks/CW-20261011-0050')
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
