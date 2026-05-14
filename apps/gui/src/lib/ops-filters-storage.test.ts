import { describe, it, expect, beforeEach, vi } from 'vitest'
import { saveOpsFilters, readOpsFilters, clearOpsFilters, type OpsFilters } from './ops-filters-storage'

// Minimal localStorage shim for the node test environment. The storage
// module reads/writes via the global `localStorage`; vitest's node env
// doesn't provide one, so we install a memory-backed impl for each test.
function installMemoryLocalStorage() {
  const store = new Map<string, string>()
  const ls = {
    getItem: (k: string) => (store.has(k) ? (store.get(k) as string) : null),
    setItem: (k: string, v: string) => {
      store.set(k, v)
    },
    removeItem: (k: string) => {
      store.delete(k)
    },
    clear: () => store.clear(),
    key: (i: number) => Array.from(store.keys())[i] ?? null,
    get length() {
      return store.size
    },
  }
  vi.stubGlobal('localStorage', ls)
}

beforeEach(() => {
  installMemoryLocalStorage()
})

describe('ops-filters-storage', () => {
  it('round-trips all filter fields including search and includeInternal', () => {
    const filters: OpsFilters = {
      statuses: ['todo', 'doing'],
      priorities: [1, 2],
      projectId: 'prj_abc',
      sprintId: 'spr_xyz',
      epicId: 'epc_123',
      tagSlug: 'infra',
      manual: 'auto',
      search: 'scheduler',
      includeInternal: true,
    }
    saveOpsFilters(filters)
    const restored = readOpsFilters()
    expect(restored).toEqual(filters)
  })

  // CW-20260503-0011: pre-existing storage blobs (without includeInternal)
  // must read back with includeInternal=false so legacy users see the same
  // hidden-by-default behavior as the backend.
  it('defaults includeInternal to false when the stored entry predates the field', () => {
    localStorage.setItem(
      'torque:ops:filters:v1',
      JSON.stringify({
        statuses: ['todo'],
        priorities: [],
        projectId: null,
        sprintId: null,
        epicId: null,
        tagSlug: null,
        manual: 'both',
        search: '',
      }),
    )
    expect(readOpsFilters()?.includeInternal).toBe(false)
  })

  it('defaults manual to "both" when the stored entry predates the field', () => {
    localStorage.setItem(
      'torque:ops:filters:v1',
      JSON.stringify({
        statuses: ['todo'],
        priorities: [],
        projectId: null,
        sprintId: null,
        epicId: null,
        tagSlug: null,
      }),
    )
    expect(readOpsFilters()?.manual).toBe('both')
  })

  it('returns null when no filters are stored', () => {
    expect(readOpsFilters()).toBeNull()
  })

  it('preserves projectId through save/read cycle (bug-regression guard)', () => {
    // Explicit projectId-focused test — the previous hydration bug caused
    // the field to appear unset on reload; confirm storage itself never
    // dropped it.
    saveOpsFilters({
      statuses: [],
      priorities: [],
      projectId: 'prj_sticky',
      sprintId: null,
      epicId: null,
      tagSlug: null,
      manual: 'both',
      search: '',
      includeInternal: false,
    })
    const restored = readOpsFilters()
    expect(restored?.projectId).toBe('prj_sticky')
  })

  it('clearOpsFilters wipes storage', () => {
    saveOpsFilters({
      statuses: ['done'],
      priorities: [3],
      projectId: 'prj_1',
      sprintId: null,
      epicId: null,
      tagSlug: null,
      manual: 'both',
      search: '',
      includeInternal: false,
    })
    clearOpsFilters()
    expect(readOpsFilters()).toBeNull()
  })

  it('sanitizes malformed stored values', () => {
    // Simulate a corrupted entry written by an older build or manual edit.
    localStorage.setItem(
      'torque:ops:filters:v1',
      JSON.stringify({
        statuses: ['todo', 42, 'doing'],
        priorities: [1, '2', 3],
        projectId: 12345,
        sprintId: 'spr_ok',
        epicId: null,
        tagSlug: null,
        mode: 'all',
      }),
    )
    const restored = readOpsFilters()
    expect(restored).toEqual({
      statuses: ['todo', 'doing'],
      priorities: [1, 3],
      projectId: null,
      sprintId: 'spr_ok',
      epicId: null,
      tagSlug: null,
      manual: 'both',
      search: '',
      includeInternal: false,
    })
  })

  it('treats non-JSON storage as absent (no throw)', () => {
    localStorage.setItem('torque:ops:filters:v1', '{not valid json')
    expect(readOpsFilters()).toBeNull()
  })

  it('migrates legacy manual="all" to "both" on read', () => {
    localStorage.setItem(
      'torque:ops:filters:v1',
      JSON.stringify({
        statuses: ['todo'],
        priorities: [],
        projectId: null,
        sprintId: null,
        epicId: null,
        tagSlug: null,
        mode: 'all',
        manual: 'all',
      }),
    )
    expect(readOpsFilters()?.manual).toBe('both')
  })

  it('drops the legacy mode field and defaults search to "" on read', () => {
    localStorage.setItem(
      'torque:ops:filters:v1',
      JSON.stringify({
        statuses: ['todo'],
        priorities: [],
        projectId: null,
        sprintId: null,
        epicId: null,
        tagSlug: null,
        mode: 'executing',
        manual: 'auto',
      }),
    )
    const restored = readOpsFilters()
    expect(restored).toBeTruthy()
    expect((restored as unknown as Record<string, unknown>).mode).toBeUndefined()
    expect(restored?.search).toBe('')
  })
})
