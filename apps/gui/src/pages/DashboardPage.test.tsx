/** @vitest-environment jsdom */
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import type { SSEEvent } from '@/lib/types'
import type { RunTimeSeries } from '@/lib/api'

const { api } = vi.hoisted(() => ({ api: { taskFacets: vi.fn(), taskRollup: vi.fn(), runFacets: vi.fn(), runTimeSeries: vi.fn(), pageRuns: vi.fn(), subscribeEvents: vi.fn() } }))
vi.mock('@/hooks/use-api', () => ({ useApi: () => api }))
vi.mock('@/components/domain/restart-frontend-button', () => ({ RestartFrontendButton: () => null }))
vi.mock('@/components/widgets', () => ({ RecentRuns: ({ runs }: { runs: { id: number; status: string }[] }) => <div>{runs.map((r) => <div key={r.id}>Run {r.id}: {r.status}</div>)}</div> }))
vi.mock('@hollis-labs/sysop-ui', () => {
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>
  return { cn: (...values: (string | boolean | undefined)[]) => values.filter(Boolean).join(" "), Card: Box, CardContent: Box, Tabs: Box, TabsContent: Box, TabsList: Box, TabsTrigger: Box, Skeleton: () => <div>Loading</div>, PageHeader: Box,
    SummaryCards: ({ cards }: { cards: { label: string; value: number }[] }) => <div>{cards.map((c) => <div key={c.label}>{c.label}: {c.value}</div>)}</div>,
    EmptyState: ({ description }: { description: string }) => <div>{description}</div>, Button: ({ children, onClick, disabled }: { children: ReactNode; onClick: () => void; disabled: boolean }) => <button onClick={onClick} disabled={disabled}>{children}</button> }
})
vi.mock('@hollis-labs/sysop-ui/widgets', () => ({
  BarMeter: ({ title, rows }: { title: string; rows: { label: string; value: number }[] }) => <div>{title}: {rows.map((r) => `${r.label}=${r.value}`).join(', ')}</div>,
  DonutChart: ({ segments }: { segments: { value: number }[] }) => <div>Run total: {segments.reduce((n, s) => n + s.value, 0)}</div>,
}))
import DashboardPage from './DashboardPage'

let event: (event: SSEEvent) => void
const facet = (dimension: string, counts: Record<string, number>) => ({ dimension, buckets: Object.entries(counts).map(([value, count]) => ({ value, count })), total_distinct: Object.keys(counts).length, returned: Object.keys(counts).length, truncated: false })
const series: RunTimeSeries = { bucket: 'day', tz_offset_minutes: 0, since: '2026-10-01T00:00:00Z', until: '2026-10-01T12:00:00Z', buckets: [{ start: '2026-10-01T00:00:00Z', count: 4000, prompt_tokens: 50000, completion_tokens: 10000, cost: 123.5, status_counts: { done: 3500, failed: 500 } }], totals: { count: 4000, prompt_tokens: 50000, completion_tokens: 10000, cost: 123.5 } }
beforeEach(() => {
  api.taskFacets.mockResolvedValue({ matching_count: 4330, facets: [facet('status', { doing: 1000, done: 2000, archived: 500, todo: 830 })] })
  api.taskRollup.mockResolvedValue({ total: 3000, scopes: [{ scope_id: 'p', total: 3000, counts: { doing: 1000, done: 2000 } }] })
  api.runFacets.mockResolvedValue({ matching_count: 4000, facets: [facet('status', { done: 3500, failed: 500 }), facet('executor', { cli: 4000 }), facet('profile', { codex: 4000 })], totals: series.totals })
  api.runTimeSeries.mockResolvedValue(series)
  api.pageRuns.mockResolvedValue({ items: [{ id: 1, status: 'running' }], meta: { has_more: true, next_cursor: 'older', limit: 12, returned: 1 } })
  api.subscribeEvents.mockImplementation((onEvent: typeof event) => { event = onEvent; return () => {} })
})
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.useRealTimers() })

describe('Dashboard aggregate data and bounded recents', () => {
  it('renders exact whole-cohort counts, tokens and ledger cost rather than the recent page', async () => {
    render(<MemoryRouter><DashboardPage /></MemoryRouter>)
    await screen.findByText('Total: 4330')
    expect(screen.getByText('Active: 1830')).toBeTruthy()
    expect(screen.getByText('Done: 2000')).toBeTruthy()
    expect(screen.getByText('Run total: 4000')).toBeTruthy()
    expect(screen.getByText('60,000 total tokens')).toBeTruthy()
    expect(screen.getByText('$123.50 total')).toBeTruthy()
    expect(screen.getByLabelText('4000 runs over 16 weeks')).toBeTruthy()
    expect(api.pageRuns).toHaveBeenCalledTimes(1)
    expect(api.pageRuns).toHaveBeenCalledWith({ limit: 12, sort_by: 'started_at', sort_dir: 'desc' })
    expect(api.runTimeSeries).toHaveBeenCalledTimes(3)
    const history = api.runTimeSeries.mock.calls[0][0]
    expect(api.runFacets.mock.calls[0][0]).toEqual({ since: history.since, until: history.until })
    expect(api.taskRollup).toHaveBeenCalledWith('project_id')
  })

  it('fetches older pages on demand while keeping only the visible page', async () => {
    api.pageRuns.mockResolvedValueOnce({ items: [{ id: 1, status: 'running' }], meta: { has_more: true, next_cursor: 'older' } })
      .mockResolvedValueOnce({ items: [{ id: 2, status: 'done' }], meta: { has_more: false, next_cursor: null } })
    render(<MemoryRouter><DashboardPage /></MemoryRouter>)
    await screen.findByText('Run 1: running')
    expect(api.pageRuns).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByText('Older runs'))
    await screen.findByText('Run 2: done')
    expect(screen.queryByText('Run 1: running')).toBeNull()
    expect(api.pageRuns.mock.calls[1][0]).toEqual({ limit: 12, sort_by: 'started_at', sort_dir: 'desc', cursor: 'older' })
    expect((screen.getByText('Older runs') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByText('Newest runs')).toBeTruthy()
    expect(api.taskFacets).toHaveBeenCalledTimes(1)
  })

  it('debounces an SSE burst into one aggregate refresh and never refetches row lists', async () => {
    render(<MemoryRouter><DashboardPage /></MemoryRouter>)
    await screen.findByText('Total: 4330')
    await waitFor(() => expect(api.subscribeEvents).toHaveBeenCalled())
    vi.useFakeTimers()
    act(() => {
      for (let i = 0; i < 40; i++) event({ type: 'task.created', data: { task_id: `outside-page-${i}` } })
      event({ type: 'run.completed', data: { run_id: 1, payload: { status: 'done' } } })
    })
    expect(screen.getByText('Run 1: done')).toBeTruthy()
    await act(async () => { vi.advanceTimersByTime(1499) })
    expect(api.taskFacets).toHaveBeenCalledTimes(1)
    await act(async () => { vi.advanceTimersByTime(1) })
    expect(api.taskFacets).toHaveBeenCalledTimes(2)
    expect(api.runFacets).toHaveBeenCalledTimes(2)
    expect(api.runTimeSeries).toHaveBeenCalledTimes(6)
    expect(api.pageRuns).toHaveBeenCalledTimes(1)
    expect(api.taskRollup).toHaveBeenCalledTimes(2)
  })
})
