// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, fireEvent, cleanup } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { TaskRow } from './task-row'
import { ApiProvider } from '@/hooks/use-api'
import {
  ActiveRunsContext,
  type ActiveRunsContextValue,
} from '@/hooks/active-runs-context'
import type { Task } from '@/lib/types'

// Tiny ResizeObserver shim for base-ui tooltip internals that may observe
// nodes during render inside jsdom.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver =
  ResizeObserverStub

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'CW-20260418-0001',
    title: 'example task',
    description: '',
    status: 'todo',
    priority: 2,
    tags: [],
    manual: true,
    executor: 'cli',
    agent_profile: '',
    working_dir: '',
    kind: 'agent',
    source_type: 'user',
    source_ref: null,
    trust: 'normal',
    checkpoint_mode: 'none',
    on_checkpoint_response: 'resume',
    tools: [],
    permissions: {},
    environment: {},
    system_prompt: '',
    files: [],
    cost_budget: null,
    max_retries: 3,
    max_duration_ms: null,
    token_budget: null,
    on_done: 'review',
    on_fail: 'retry',
    on_review: 'pause',
    on_done_merge: 'none',
    escalation_chain: [],
    quality_gates: [],
    deliverables: [],
    deliverable_preset: '',
    depends_on: [],
    blocked_reason: '',
    metadata: {},
    sprint_id: null,
    project_id: null,
    epic_id: null,
    created_at: '2026-04-17T00:00:00Z',
    updated_at: '2026-04-17T00:00:00Z',
    ...overrides,
  }
}

function renderRow(opts: {
  task?: Task
  selected?: boolean
  onSelect?: (id: string, selected: boolean) => void
}) {
  const task = opts.task ?? makeTask()
  const activeRuns: ActiveRunsContextValue = {
    activeRuns: new Map(),
    connected: true,
  }
  const utils = render(
    <ApiProvider>
      <ActiveRunsContext.Provider value={activeRuns}>
        <MemoryRouter initialEntries={[`/tasks?view=list`]}>
          <Routes>
            <Route
              path="/tasks"
              element={
                <table>
                  <tbody>
                    <TaskRow
                      task={task}
                      selected={opts.selected}
                      onSelect={opts.onSelect}
                    />
                  </tbody>
                </table>
              }
            />
            <Route
              path="/tasks/:id"
              element={<div data-testid="task-detail-route">detail</div>}
            />
          </Routes>
        </MemoryRouter>
      </ActiveRunsContext.Provider>
    </ApiProvider>,
  )
  return utils
}

describe('TaskRow — row click deterministically opens detail view', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
  })
  afterEach(() => {
    vi.useRealTimers()
    cleanup()
  })

  it('clicking the row body navigates to the task detail route', () => {
    const { getByTestId, queryByTestId } = renderRow({
      onSelect: vi.fn(),
    })
    expect(queryByTestId('task-detail-route')).toBeNull()
    const row = getByTestId('task-row')
    // Clicking the row itself (e.g. the non-interactive cell area) must
    // navigate — this is the core assertion for CW-20260418-0009.
    fireEvent.click(row, { button: 0 })
    expect(queryByTestId('task-detail-route')).not.toBeNull()
  })

  it('clicking a non-interactive cell (priority) navigates via row handler', () => {
    // The priority <td> is not marked data-row-interactive. A click anywhere
    // in it must bubble to the row and navigate. This guards against the
    // previous bug where several <td>s stopped propagation and silently
    // swallowed the click.
    const { container, queryByTestId } = renderRow({ onSelect: vi.fn() })
    // Find the priority cell by its class signature used in task-row.tsx.
    const priCell = container.querySelector(
      'td.w-px.whitespace-nowrap.px-1\\.5.py-1\\.5',
    )
    expect(priCell).not.toBeNull()
    fireEvent.click(priCell as Element, { button: 0, bubbles: true })
    expect(queryByTestId('task-detail-route')).not.toBeNull()
  })

  it('clicking the checkbox toggles selection and does NOT navigate', () => {
    const onSelect = vi.fn()
    const { getByTestId, queryByTestId } = renderRow({ onSelect })

    const checkbox = getByTestId('task-row-checkbox') as HTMLInputElement
    fireEvent.click(checkbox, { button: 0 })

    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect).toHaveBeenCalledWith('CW-20260418-0001', true)
    // The row handler must not navigate when the click lands on the
    // checkbox (marked data-row-interactive="true").
    expect(queryByTestId('task-detail-route')).toBeNull()
  })

  it('clicking the actions menu trigger does NOT navigate', () => {
    // The kebab menu trigger sits inside a span[data-row-interactive=true].
    // A primary click on it must not trigger row navigation.
    const { getByTestId, queryByTestId } = renderRow({ onSelect: vi.fn() })

    const trigger = getByTestId('task-row-actions-trigger')
    fireEvent.click(trigger, { button: 0 })

    expect(queryByTestId('task-detail-route')).toBeNull()
  })

  it('cmd-click on the row does not trigger the row-level navigation', () => {
    // Modifier clicks are reserved for the inner <Link> to handle (e.g.
    // "open in new tab"). The row handler must bail out so it can't hijack
    // those semantics.
    const { getByTestId, queryByTestId } = renderRow({ onSelect: vi.fn() })

    fireEvent.click(getByTestId('task-row'), { button: 0, metaKey: true })
    expect(queryByTestId('task-detail-route')).toBeNull()
  })

  it('pressing Enter while the row is focused navigates', () => {
    const { getByTestId, queryByTestId } = renderRow({ onSelect: vi.fn() })

    const row = getByTestId('task-row') as HTMLTableRowElement
    row.focus()
    fireEvent.keyDown(row, { key: 'Enter' })
    expect(queryByTestId('task-detail-route')).not.toBeNull()
  })
})
