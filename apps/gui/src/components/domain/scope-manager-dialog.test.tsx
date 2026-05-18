// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ApiProvider } from '@/hooks/use-api'
import { ScopeManagerDialog } from './scope-manager-dialog'

const projectA = {
  id: 'PRJ-1',
  name: 'Alpha',
  description: 'Alpha desc',
  repo_path: '/alpha',
  agent_path: '',
  read_paths: [],
  write_paths: [],
  context_paths: [],
  permissions: {},
  rules: [],
  status: 'active',
  icon: '',
  created_at: '2026-05-01T00:00:00Z',
  updated_at: '2026-05-01T00:00:00Z',
}

const projectB = {
  ...projectA,
  id: 'PRJ-2',
  name: 'Beta',
  description: 'Beta desc',
  repo_path: '/beta',
  updated_at: '2026-05-01T01:00:00Z',
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('ScopeManagerDialog', () => {
  afterEach(() => {
    cleanup()
  })

  beforeEach(() => {
    vi.restoreAllMocks()
    globalThis.fetch = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/projects')) return Promise.resolve(jsonResponse({ projects: [projectA, projectB] }))
      if (url.endsWith('/epics')) return Promise.resolve(jsonResponse({ epics: [] }))
      if (url.endsWith('/sprints')) return Promise.resolve(jsonResponse({ sprints: [] }))
      if (url.endsWith('/projects/PRJ-1/artifacts')) return Promise.resolve(jsonResponse({ artifacts: [] }))
      if (url.endsWith('/projects/PRJ-2/artifacts')) return Promise.resolve(jsonResponse({ artifacts: [] }))
      return Promise.reject(new Error(`Unhandled fetch: ${url}`))
    }) as unknown as typeof fetch
  })

  function renderDialog() {
    return render(
      <MemoryRouter>
        <ApiProvider>
          <ScopeManagerDialog
            open
            onOpenChange={() => {}}
            flags={{ projects: true, epics: true, sprints: true }}
          />
        </ApiProvider>
      </MemoryRouter>
    )
  }

  it('switches the detail pane when a different project is clicked', async () => {
    renderDialog()

    await screen.findByDisplayValue('Alpha')
    fireEvent.click(screen.getByRole('button', { name: /Beta/i }))

    await waitFor(() => {
      expect(screen.getByDisplayValue('Beta')).toBeTruthy()
      expect(screen.getByDisplayValue('/beta')).toBeTruthy()
    })
  })

  it('clears the detail pane for creating a new project when plus is clicked', async () => {
    renderDialog()

    await screen.findByDisplayValue('Alpha')
    fireEvent.click(screen.getByRole('button', { name: 'Create project' }))

    await waitFor(() => {
      const nameInput = screen.getAllByPlaceholderText('Project name').find((el) => (el as HTMLInputElement).value === '')
      const pathInput = screen.getAllByPlaceholderText('/Users/you/Projects/app').find((el) => (el as HTMLInputElement).value === '')
      expect(nameInput).toBeTruthy()
      expect(pathInput).toBeTruthy()
    })
  })
})
