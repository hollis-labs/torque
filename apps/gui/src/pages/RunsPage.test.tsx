/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import type { RunPage } from '@/lib/api'

const { pageRuns, getRun } = vi.hoisted(() => ({ pageRuns: vi.fn(), getRun: vi.fn() }))
const api = { pageRuns, getRun }
vi.mock('@/hooks/use-api', () => ({ useApi: () => api }))
vi.mock('@/hooks/use-sse', () => ({ useSSE: () => ({ lastEvent: null, connected: true }) }))
vi.mock('@/components/domain/run-card', () => ({ RunCard: ({ run }: { run: { id: number } }) => <div>Run {run.id}</div> }))
vi.mock('@hollis-labs/sysop-ui', () => ({
  Skeleton: () => <div>Loading</div>,
  EmptyState: ({ title }: { title: string }) => <div>{title}</div>,
  Select: ({ value, onValueChange, children }: { value: string; onValueChange: (value: string) => void; children: ReactNode }) => <select value={value} onChange={(event) => onValueChange(event.target.value)}>{children}</select>,
  SelectContent: ({ children }: { children: ReactNode }) => <>{children}</>,
  SelectItem: ({ value, children }: { value: string; children: ReactNode }) => <option value={value}>{children}</option>,
  SelectTrigger: () => null,
  SelectValue: () => null,
}))
import RunsPage from './RunsPage'

const page = (ids: number[], cursor: string | null): RunPage => ({
  items: ids.map((id) => ({ id, status: 'done' })) as RunPage['items'],
  meta: { returned: ids.length, limit: 50, has_more: !!cursor, next_cursor: cursor },
})
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.unstubAllGlobals() })

function observerStub() {
  vi.stubGlobal('IntersectionObserver', class {
    observe() {}
    disconnect() {}
  })
}

describe('RunsPage cursor browsing', () => {
  it('loads only the first page and appends on demand; Completed is a server done filter', async () => {
    observerStub()
    pageRuns.mockResolvedValueOnce(page([1], 'next')).mockResolvedValueOnce(page([2], null)).mockResolvedValueOnce(page([3], null))
    render(<RunsPage />)
    await screen.findByText('Run 1')
    expect(pageRuns).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByText('Load older runs'))
    await screen.findByText('Run 2')
    expect(screen.getByText('Run 1')).toBeTruthy()
    expect(pageRuns.mock.calls[1][0]).toMatchObject({ cursor: 'next' })
    fireEvent.change(screen.getAllByRole('combobox')[1], { target: { value: 'completed' } })
    await screen.findByText('Run 3')
    expect(pageRuns.mock.calls[2][0]).toEqual({ status: 'done', sort_by: 'started_at' })
    expect(screen.queryByText('Run 1')).toBeNull()
  })

  it('discards an old continuation response after the filter changes', async () => {
    observerStub()
    let finish: (value: RunPage) => void = () => {}
    pageRuns.mockResolvedValueOnce(page([1], 'old-cursor'))
      .mockImplementationOnce(() => new Promise<RunPage>((resolve) => { finish = resolve }))
      .mockResolvedValueOnce(page([3], null))
    render(<RunsPage />)
    await screen.findByText('Run 1')
    fireEvent.click(screen.getByText('Load older runs'))
    await waitFor(() => expect(pageRuns).toHaveBeenCalledTimes(2))
    fireEvent.change(screen.getAllByRole('combobox')[1], { target: { value: 'failed' } })
    await screen.findByText('Run 3')
    await act(async () => finish(page([2], null)))
    expect(screen.queryByText('Run 2')).toBeNull()
    expect(screen.getByText('Run 3')).toBeTruthy()
  })
})
