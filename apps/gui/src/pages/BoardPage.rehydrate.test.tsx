/**
 * @vitest-environment jsdom
 *
 * Mount → unmount → remount reproduction for the filter-rehydrate-on-nav
 * bug. The filter-persistence hydration (9ae2e10) + hydration-race fix
 * (bf21382) made the initial hard-refresh case work, but filters stopped
 * re-applying when navigating back to /operations in-app (task detail →
 * back). This test simulates that lifecycle and asserts the URL picks up
 * stored filters on each distinct navigation arrival.
 *
 * Render target is jsdom via the per-file env pragma above; the rest of
 * the suite still runs in the default node env.
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { act, useEffect } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter, Routes, Route, useLocation, useNavigate } from 'react-router-dom'
import BoardPage from './BoardPage'
import { ApiProvider } from '@/hooks/use-api'
import { saveOpsFilters } from '@/lib/ops-filters-storage'

// Opt into React's test-only act() environment so effect-flushing doesn't
// spam console warnings. Vitest's jsdom env doesn't set this by default.
;(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// Stub fetch so useApi's requests resolve with empty bodies. We only care
// about filter/URL plumbing, not data rendering.
function installStubFetch() {
  const stub = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url)
    let body: unknown = {}
    if (url.includes('/tasks')) body = { tasks: [] }
    else if (url.includes('/projects')) body = { projects: [] }
    else if (url.includes('/sprints')) body = { sprints: [] }
    else if (url.includes('/epics')) body = { epics: [] }
    else if (url.includes('/tags')) body = { tags: [] }
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

// useSSE opens an EventSource; we don't care about the stream, only that
// construction doesn't throw.
function installStubEventSource() {
  class FakeEventSource {
    url: string
    onmessage: ((ev: MessageEvent) => void) | null = null
    onerror: ((ev: Event) => void) | null = null
    onopen: ((ev: Event) => void) | null = null
    readyState = 0
    constructor(url: string) {
      this.url = url
    }
    addEventListener() {}
    removeEventListener() {}
    close() {}
  }
  vi.stubGlobal('EventSource', FakeEventSource)
}

// Inline probe component that publishes the current search string to the
// test via callback — sidesteps DOM scraping.
function LocationTap({ onChange }: { onChange: (search: string) => void }) {
  const loc = useLocation()
  onChange(loc.search)
  return null
}

// Exposes the router `navigate` function to the test so we can drive
// in-app transitions without racing through user events. Written via
// effect (not directly in render) so the React compiler's purity lint
// stays happy.
let capturedNavigate: ReturnType<typeof useNavigate> | null = null
function NavigateTap() {
  const navigate = useNavigate()
  useEffect(() => {
    capturedNavigate = navigate
  }, [navigate])
  return null
}

function renderAppShell(
  container: HTMLElement,
  initialEntry: string,
  onLocation: (search: string) => void,
): Root {
  const root = createRoot(container)
  act(() => {
    root.render(
      <ApiProvider baseUrl="/api/v1">
        <MemoryRouter initialEntries={[initialEntry]}>
          <Routes>
            <Route path="/operations" element={<BoardPage />} />
            {/* Stand-in for /tasks/:id so we can navigate away without
                mounting the real detail page. */}
            <Route path="/tasks/:id" element={<div>stub detail</div>} />
          </Routes>
          <LocationTap onChange={onLocation} />
          <NavigateTap />
        </MemoryRouter>
      </ApiProvider>,
    )
  })
  return root
}

describe('BoardPage filter rehydration on remount', () => {
  let container: HTMLElement

  beforeEach(() => {
    localStorage.clear()
    installStubFetch()
    installStubEventSource()
    capturedNavigate = null
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(() => {
    container.remove()
    vi.unstubAllGlobals()
  })

  it('restores filters from localStorage on a fresh mount with empty URL', async () => {
    saveOpsFilters({
      statuses: ['todo'],
      priorities: [1],
      projectId: 'prj_abc',
      sprintId: null,
      epicId: null,
      tagSlug: null,
      mode: 'all',
      manual: 'all',
    })

    let observedSearch = ''
    const root = renderAppShell(container, '/operations', (s) => {
      observedSearch = s
    })

    await act(async () => {
      await Promise.resolve()
    })

    expect(observedSearch).toContain('status=todo')
    expect(observedSearch).toContain('priority=1')
    expect(observedSearch).toContain('project_id=prj_abc')

    act(() => root.unmount())
  })

  it('re-applies filters after in-app nav to task detail and back', async () => {
    // User had status=todo on a previous visit — storage persisted it.
    saveOpsFilters({
      statuses: ['todo'],
      priorities: [],
      projectId: null,
      sprintId: null,
      epicId: null,
      tagSlug: null,
      mode: 'all',
      manual: 'all',
    })

    let observedSearch = ''
    const root = renderAppShell(container, '/operations', (s) => {
      observedSearch = s
    })
    await act(async () => {
      await Promise.resolve()
    })
    // Initial hydration restored the URL.
    expect(observedSearch).toContain('status=todo')

    // Navigate to the task detail (no query params — same as task-row
    // `navigate('/tasks/:id')`). BoardPage unmounts.
    act(() => {
      capturedNavigate!('/tasks/CW-DEMO-1')
    })
    await act(async () => {
      await Promise.resolve()
    })
    expect(observedSearch).toBe('')

    // Navigate back to /operations with no query string — equivalent to
    // clicking the "Operations" breadcrumb link. BoardPage mounts again.
    act(() => {
      capturedNavigate!('/operations')
    })
    await act(async () => {
      await Promise.resolve()
    })

    // REGRESSION GUARD: the second arrival at /operations must pick up
    // the stored filter even though the URL arrived empty.
    expect(observedSearch).toContain('status=todo')

    act(() => root.unmount())
  })

  it('re-applies filters when BoardPage stays mounted across URL changes', async () => {
    // This guards the second failure mode from the bug report: a scenario
    // where BoardPage isn't unmounted between navigations (e.g. a future
    // persistent route shell, or a layout that keeps the page alive).
    // Under the old one-shot-flag hydration this case silently regressed
    // because the flag stayed `true` past the first restoration.
    saveOpsFilters({
      statuses: ['doing'],
      priorities: [],
      projectId: null,
      sprintId: null,
      epicId: null,
      tagSlug: null,
      mode: 'all',
      manual: 'all',
    })

    // Render BoardPage directly (no Routes switch) so it stays mounted
    // even as the URL changes underneath it.
    const root = createRoot(container)
    let observedSearch = ''
    act(() => {
      root.render(
        <ApiProvider baseUrl="/api/v1">
          <MemoryRouter initialEntries={['/operations']}>
            <BoardPage />
            <LocationTap onChange={(s) => (observedSearch = s)} />
            <NavigateTap />
          </MemoryRouter>
        </ApiProvider>,
      )
    })
    await act(async () => {
      await Promise.resolve()
    })
    expect(observedSearch).toContain('status=doing')

    // Simulate user clearing the URL manually (same as clicking Operations
    // nav while BoardPage is alive). Storage still has the filter.
    act(() => {
      capturedNavigate!('/operations')
    })
    await act(async () => {
      await Promise.resolve()
    })

    // REGRESSION GUARD: second arrival on /operations (with the
    // BoardPage instance reused) must re-hydrate from storage.
    expect(observedSearch).toContain('status=doing')

    act(() => root.unmount())
  })

  it('URL filter params win over storage (deep-link case)', async () => {
    saveOpsFilters({
      statuses: ['todo'],
      priorities: [],
      projectId: null,
      sprintId: null,
      epicId: null,
      tagSlug: null,
      mode: 'all',
      manual: 'all',
    })

    let observedSearch = ''
    const root = renderAppShell(container, '/operations?status=done', (s) => {
      observedSearch = s
    })
    await act(async () => {
      await Promise.resolve()
    })

    // URL said status=done; storage had status=todo — URL wins.
    expect(observedSearch).toContain('status=done')
    expect(observedSearch).not.toContain('status=todo')

    act(() => root.unmount())
  })
})
