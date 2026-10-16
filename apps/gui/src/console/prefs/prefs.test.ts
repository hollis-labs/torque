// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest'
import { ALERT_PREFS_STORAGE_KEY, LocalAlertPrefsStore } from '../alerts/prefs'
import { ADMIN_CONTRACT_VERSION, PREF_FIELD_KEYS, changesToPrefsPatch, consolePrefsSettingsGroup, prefsToSnapshotValues } from './manifest'
import { inQuietHours, sanitizePrefs } from './sanitize'
import { LocalConsolePrefsStore, MemoryConsolePrefsStore } from './store'
import { DEFAULT_CONSOLE_PREFS } from './types'

beforeEach(() => window.localStorage.clear())

describe('ConsolePrefs store', () => {
  it('reads A1 legacy entries and fills new fields with defaults', () => {
    window.localStorage.setItem(ALERT_PREFS_STORAGE_KEY, JSON.stringify({ quiet: true, toastMinSeverity: 'error' }))
    const s = new LocalAlertPrefsStore()
    expect(s.get()).toEqual({ ...DEFAULT_CONSOLE_PREFS, quiet: true, toastMinSeverity: 'error' })
  })
  it('sanitises junk and notifies subscribers', () => {
    expect(sanitizePrefs({ quietHoursStart: '25:99', coalesceWindowSec: 1e9, mutedSources: [' A ', 3, 'a', ''] })).toMatchObject({ quietHoursStart: '22:00', coalesceWindowSec: 3600, mutedSources: ['a'] })
    const s = new LocalConsolePrefsStore('k')
    let n = 0
    s.subscribe(() => n++)
    s.set({ mutedSources: ['Bravo'], coalesceWindowSec: 0 })
    expect(n).toBe(1)
    expect(JSON.parse(window.localStorage.getItem('k')!)).toMatchObject({ mutedSources: ['bravo'], coalesceWindowSec: 0 })
    expect(new MemoryConsolePrefsStore().get()).toEqual(DEFAULT_CONSOLE_PREFS)
  })
  it('quiet hours window logic', () => {
    const p = { quietHoursEnabled: true, quietHoursStart: '09:00', quietHoursEnd: '17:00' }
    expect(inQuietHours(p, new Date(2026, 0, 1, 9, 0))).toBe(true)
    expect(inQuietHours(p, new Date(2026, 0, 1, 17, 0))).toBe(false)
    expect(inQuietHours({ ...p, quietHoursEnd: '09:00' }, new Date(2026, 0, 1, 9, 0))).toBe(false)
    expect(inQuietHours({ ...p, quietHoursEnabled: false }, new Date(2026, 0, 1, 10, 0))).toBe(false)
  })
})

describe('admin-manifest settings group', () => {
  const g = consolePrefsSettingsGroup('u1')
  it('is a structurally valid v1 group', () => {
    expect(ADMIN_CONTRACT_VERSION).toBe(1)
    expect(g.section).toBe('settings')
    expect(g.id).toMatch(/^[a-z0-9][a-z0-9_.-]*$/)
    const props = Object.keys(g.schema.properties)
    expect(Object.keys(g.fields).sort()).toEqual(props.sort())
    expect(props.sort()).toEqual(Object.keys(PREF_FIELD_KEYS).sort())
    for (const k of props) {
      expect(k).toMatch(/^[a-z0-9][a-z0-9_.-]*$/)
      expect(['string', 'boolean', 'integer', 'number']).toContain(g.schema.properties[k].type)
      expect(g.fields[k].editable).toBe(true)
    }
    for (const cap of ['read', 'validate', 'update', 'reset'] as const) expect(g.endpoints[cap]).toBeDefined()
    expect(g.endpoints.read?.method).toBe('GET')
  })
  it('projects prefs to snapshot values and applies kit-settings changes', () => {
    const v = prefsToSnapshotValues({ ...DEFAULT_CONSOLE_PREFS, mutedSources: ['a', 'b'] })
    expect(v.muted_sources.value).toBe('a, b')
    expect(v.quiet).toMatchObject({ present: true, value: false, editable: true })
    const patch = changesToPrefsPatch({ set: { quiet: true, muted_sources: 'X, y', nope: 1 }, unset: ['coalesce_window_sec'] }, DEFAULT_CONSOLE_PREFS)
    expect(patch).toEqual({ quiet: true, mutedSources: ['x', 'y'], coalesceWindowSec: 120 })
  })
})
