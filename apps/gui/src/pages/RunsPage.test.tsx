/** @vitest-environment jsdom */
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import type { SSEEvent } from '@/lib/types'

const { api } = vi.hoisted(() => ({ api: { runFacets: vi.fn(), pageRuns: vi.fn(), subscribeEvents: vi.fn() } }))
vi.mock('@/hooks/use-api', () => ({ useApi: () => api }))
vi.mock('@/components/domain/run-card', () => ({ RunCard: ({ run }: { run: { id: number; status: string } }) => <div>Run {run.id}: {run.status}</div> }))
vi.mock('@hollis-labs/sysop-ui', () => ({
  Skeleton: () => <div>Loading</div>,
  Button: ({ children, onClick, disabled, type }: { children: ReactNode; onClick?: () => void; disabled?: boolean; type?: 'button' | 'submit' }) => <button type={type ?? 'button'} onClick={onClick} disabled={disabled}>{children}</button>,
  SummaryCards: ({ cards }: { cards: { label: string; value: number }[] }) => <div>{cards.map((card) => <div key={card.label}>{card.label}: {card.value}</div>)}</div>,
  EmptyState: ({ title, description }: { title: string; description: string }) => <div>{title}: {description}</div>,
}))
import RunsPage from './RunsPage'

let event: (event: SSEEvent) => void
const totals = (count = 10001) => ({ matching_count: count, facets: [
  { dimension: 'status', buckets: [{ value: 'running', count: 1 }, { value: 'done', count: count - 1 }] },
  { dimension: 'executor', buckets: [{ value: 'cli', count }] },
  { dimension: 'profile', buckets: [{ value: 'codex', count }] },
], totals: { cost: 123.45, prompt_tokens: 50000, completion_tokens: 10000 } })
const page = (id = 1, next: string | null = 'older') => ({ items: [{ id, task_id: 'task-1', status: 'running' }], meta: { returned: 1, limit: 50, has_more: !!next, next_cursor: next } })
beforeEach(() => {
  api.runFacets.mockResolvedValue(totals())
  api.pageRuns.mockResolvedValue(page())
  api.subscribeEvents.mockImplementation((handler: typeof event) => { event = handler; return () => {} })
  vi.stubGlobal('IntersectionObserver', class { observe() {} disconnect() {} })
})
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

describe('RunsPage server cohort and shared paging', () => {
  it('uses exact facet headers, passes filters/sorts to the server and requests totals only on opt-in', async () => {
    render(<MemoryRouter><RunsPage /></MemoryRouter>)
    await screen.findByText('Run 1: running')
    expect(screen.getByText('Matching: 10001')).toBeTruthy()
    expect(screen.getByText('Completed: 10000')).toBeTruthy()
    expect(screen.getByText('60,000 tokens · $123.45 ledger cost')).toBeTruthy()
    expect(api.pageRuns.mock.calls[0][0]).toEqual({ limit: 50, sort_by: 'started_at', sort_dir: 'desc' })
    expect(api.pageRuns.mock.calls[0][1]).toBeInstanceOf(AbortSignal)
    fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'done' } })
    fireEvent.change(screen.getByLabelText('Executors'), { target: { value: 'cli,api' } })
    fireEvent.change(screen.getByLabelText('Current task profiles'), { target: { value: 'codex,claude' } })
    fireEvent.change(screen.getByLabelText('Run start window'), { target: { value: '7d' } })
    expect(api.pageRuns).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByText('Apply filters'))
    await waitFor(() => expect(api.pageRuns).toHaveBeenCalledTimes(2))
    const filtered = api.pageRuns.mock.calls[1][0]
    expect(filtered).toMatchObject({ status: 'done', executor: 'cli,api', profile: 'codex,claude', limit: 50 })
    expect(Date.parse(filtered.until) - Date.parse(filtered.since)).toBe(7 * 24 * 60 * 60 * 1000)
    const cohort = { status: filtered.status, executor: filtered.executor, profile: filtered.profile, since: filtered.since, until: filtered.until }
    expect(api.runFacets.mock.calls.at(-1)?.[0]).toEqual(cohort)
    // Server page rows are authoritative; no browser status/executor/profile filtering.
    expect(screen.getByText('Run 1: running')).toBeTruthy()
    fireEvent.change(screen.getByLabelText('Sort'), { target: { value: 'cost' } })
    await waitFor(() => expect(api.pageRuns.mock.calls.at(-1)?.[0].sort_by).toBe('cost'))
    fireEvent.change(screen.getByLabelText('Direction'), { target: { value: 'asc' } })
    await waitFor(() => expect(api.pageRuns.mock.calls.at(-1)?.[0].sort_dir).toBe('asc'))
    expect(api.pageRuns.mock.calls.every(([params]) => !params.include_total)).toBe(true)
    api.pageRuns.mockResolvedValue({ ...page(), meta: { ...page().meta, total: 10001 } })
    fireEvent.click(screen.getByLabelText('Show total progress'))
    await screen.findByText('1 loaded of 10,001')
    expect(api.pageRuns.mock.calls.at(-1)?.[0]).toMatchObject({ include_total: true, status: 'done', executor: 'cli,api', profile: 'codex,claude', sort_by: 'cost', sort_dir: 'asc' })
  })

  it('loads cursor pages on demand and ignores responses from an abandoned filter cohort', async () => {
    api.pageRuns.mockResolvedValueOnce(page()).mockResolvedValueOnce(page(2, 'more'))
    render(<MemoryRouter><RunsPage /></MemoryRouter>)
    await screen.findByText('Run 1: running')
    expect(api.pageRuns).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByText('Load older runs'))
    await screen.findByText('Run 2: running')
    expect(screen.getByText('Run 1: running')).toBeTruthy()
    expect(api.pageRuns.mock.calls[1][0]).toMatchObject({ cursor: 'older', limit: 50 })
    let resolveOld!: (value: ReturnType<typeof page>) => void
    api.pageRuns.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve })).mockResolvedValueOnce(page(4, null))
    fireEvent.click(screen.getByText('Load older runs'))
    await waitFor(() => expect(api.pageRuns).toHaveBeenCalledTimes(3))
    const oldSignal = api.pageRuns.mock.calls[2][1]
    fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'failed' } })
    fireEvent.click(screen.getByText('Apply filters'))
    await screen.findByText('Run 4: running')
    expect(oldSignal.aborted).toBe(true)
    await act(async () => resolveOld(page(3)))
    expect(screen.queryByText('Run 3: running')).toBeNull()
    expect(screen.queryByText('Run 1: running')).toBeNull()
  })

  it('coalesces SSE facet refreshes without fetching row pages or filtering loaded rows', async () => {
    render(<MemoryRouter><RunsPage /></MemoryRouter>)
    await screen.findByText('Run 1: running')
    vi.useFakeTimers()
    api.runFacets.mockResolvedValue(totals(10002))
    act(() => { for (let i = 0; i < 40; i++) event({ type: 'run.completed', data: { run_id: 1, payload: { status: 'done' } } } as SSEEvent) })
    expect(screen.getByText('Loaded runs changed. Refresh to update this page.')).toBeTruthy()
    expect(screen.getByText('Run 1: running')).toBeTruthy()
    expect((screen.getByText('Load older runs') as HTMLButtonElement).disabled).toBe(true)
    await act(async () => { vi.advanceTimersByTime(1500); await Promise.resolve() })
    expect(api.runFacets).toHaveBeenCalledTimes(2)
    expect(api.pageRuns).toHaveBeenCalledTimes(1)
    expect(screen.getByText('Matching: 10002')).toBeTruthy()
    vi.useRealTimers()
    api.pageRuns.mockResolvedValue(page(2, null))
    fireEvent.click(screen.getByText('Refresh'))
    await screen.findByText('Run 2: running')
    expect(screen.queryByText('Loaded runs changed. Refresh to update this page.')).toBeNull()
    act(() => event({ type: 'task.updated', data: { task_id: 'task-1' } } as SSEEvent))
    expect(screen.getByText('Loaded runs changed. Refresh to update this page.')).toBeTruthy()
    expect(api.pageRuns).toHaveBeenCalledTimes(2)
  })
})
