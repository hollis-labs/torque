/** @vitest-environment jsdom */
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ListPage, ModelEntry } from '@/lib/types'
const { listModels } = vi.hoisted(() => ({ listModels: vi.fn() }))
vi.mock('@/hooks/use-api', () => ({ useApi: () => ({ listModels }) }))
import ModelsPage from './ModelsPage'
const page = (name: string, next_cursor: string | null): ListPage<ModelEntry> => ({
  items: [{ id: name, name, provider_id: 'peer', cost: { input: 2, output: 3 }, limit: { context_window: 2000, max_output_tokens: 1000 } } as ModelEntry],
  meta: { returned: 1, total: 501, limit: 50, has_more: !!next_cursor, next_cursor },
})
afterEach(() => { cleanup(); vi.resetAllMocks() })
it('sends numeric sort and search to the server and resets continuation', async () => {
  listModels.mockResolvedValueOnce(page('First page', 'next')).mockResolvedValueOnce(page('Next page', null)).mockResolvedValueOnce(page('Cheap model', 'cost-next')).mockResolvedValueOnce(page('Needle beyond page', null))
  render(<ModelsPage />)
  await screen.findByText('First page')
  expect(listModels).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole('button', { name: 'Load more models' }))
  await screen.findByText('Next page')
  expect(listModels.mock.calls[1][1]).toMatchObject({ cursor: 'next' })
  fireEvent.click(screen.getByText('$/M In'))
  await screen.findByText('Cheap model')
  expect(listModels.mock.calls[2][1]).toMatchObject({ sort_by: 'cost', sort_dir: 'asc', cursor: undefined })
  expect(screen.queryByText('First page')).toBeNull()
  fireEvent.change(screen.getByPlaceholderText('Search id or name…'), { target: { value: 'needle' } })
  await screen.findByText('Needle beyond page')
  await waitFor(() => expect(listModels.mock.calls[3][1]).toMatchObject({ search: 'needle', sort_by: 'cost', cursor: undefined }))
})
