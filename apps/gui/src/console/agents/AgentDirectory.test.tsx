/** @vitest-environment jsdom */
import { afterEach, expect, it } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AgentDirectoryView } from './AgentDirectory'
import { AgentsClientProvider } from './context'
import type { AgentsClient } from './client'
import { filterAgents, isPrivileged, policySummary } from './filter'
import { createMockAgentsClient, MOCK_AGENTS } from './mock'

afterEach(cleanup)

const NOW = Date.parse('2026-10-16T05:00:00Z')
const view = (client: AgentsClient) =>
  render(
    <MemoryRouter>
      <AgentsClientProvider client={client}><AgentDirectoryView now={NOW} /></AgentsClientProvider>
    </MemoryRouter>,
  )

it('filters by search and provider and sorts live agents first', () => {
  expect(filterAgents(MOCK_AGENTS, { provider: 'opencode' }).map((a) => a.identity.id)).toEqual(['lead-proxy'])
  expect(filterAgents(MOCK_AGENTS, { search: 'REVIEWER' }).map((a) => a.identity.id)).toEqual(['team-reviewer'])
  expect(filterAgents(MOCK_AGENTS, { role: 'worker' })).toHaveLength(1)
  expect(filterAgents(MOCK_AGENTS)[0].live?.state).toBe('waiting')
})

it('summarises policy and flags privileged agents', () => {
  const rc = MOCK_AGENTS.find((a) => a.identity.id === 'release-captain')!
  expect(isPrivileged(rc)).toBe(true)
  expect(policySummary(rc)).toContain('2 privileged scopes')
  expect(isPrivileged(MOCK_AGENTS.find((a) => a.identity.id === 'team-reviewer')!)).toBe(false)
})

it('mock client returns null for an unknown id', async () => {
  expect(await createMockAgentsClient().getAgent('nope')).toBeNull()
})

it('lists agents, selects one and shows its detail with a fleet link', async () => {
  view(createMockAgentsClient())
  const list = await screen.findByRole('list', { name: 'Agents' })
  expect(within(list).getAllByRole('button')).toHaveLength(MOCK_AGENTS.length)
  fireEvent.click(within(list).getByText('Release Captain'))
  const detail = screen.getByRole('article', { name: 'Release Captain detail' })
  expect(within(detail).getByText('catalog/agents/release-captain.yaml')).toBeTruthy()
  expect(within(detail).getByText('session.write')).toBeTruthy()
  expect(within(detail).getByText('NOT RUNNING')).toBeTruthy()
  expect(within(detail).queryByRole('button')).toBeNull()
  fireEvent.click(within(list).getByText('Team Worker'))
  expect(screen.getByRole('link', { name: 'open in fleet' }).getAttribute('href')).toBe('/planner/agents')
})

it('narrows the list from the search box and shows an empty state', async () => {
  view(createMockAgentsClient())
  await screen.findByRole('list', { name: 'Agents' })
  fireEvent.change(screen.getByLabelText('Search agents'), { target: { value: 'zzz' } })
  expect(screen.getByText('No agents match')).toBeTruthy()
})

it('shows an error state when the client fails', async () => {
  const bad: AgentsClient = { ...createMockAgentsClient(), listAgents: () => Promise.reject(new Error('boom')), getAgent: () => Promise.resolve(null) }
  view(bad)
  await waitFor(() => expect(screen.getByText('boom')).toBeTruthy())
})
