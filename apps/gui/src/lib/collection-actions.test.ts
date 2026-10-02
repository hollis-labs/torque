import { expect, it, vi } from 'vitest'
import { runCollectionActions } from './collection-actions'
it('stops scheduling after failure and reports completed and unattempted IDs', async () => {
  const pending = new Map<string, { resolve: () => void; reject: (error: Error) => void }>()
  const action = vi.fn((id: string) => new Promise<void>((resolve, reject) => { pending.set(id, { resolve, reject }) }))
  const work = runCollectionActions(Array.from({ length: 10 }, (_, index) => String(index)), action)
  expect(action.mock.calls.map(([id]) => id)).toEqual(['0', '1', '2', '3'])
  pending.get('0')!.reject(new Error('membership changed'))
  await Promise.resolve()
  for (const id of ['1', '2', '3']) pending.get(id)!.resolve()
  const result = await work
  expect(result.succeeded.sort()).toEqual(['1', '2', '3'])
  expect(result.failed).toEqual([{ id: '0', message: 'membership changed' }])
  expect(result.notStarted).toEqual(['4', '5', '6', '7', '8', '9'])
  expect(action).toHaveBeenCalledTimes(4)
})
it('completes explicit selections with at most four concurrent mutations', async () => {
  let active = 0, peak = 0
  const ids = Array.from({ length: 19 }, (_, index) => `T-${index}`)
  const result = await runCollectionActions(ids, async () => { active++; peak = Math.max(peak, active); await Promise.resolve(); active-- })
  expect(peak).toBe(4)
  expect(result.succeeded.sort()).toEqual(ids.sort())
  expect(result.failed).toEqual([])
  expect(result.notStarted).toEqual([])
})
