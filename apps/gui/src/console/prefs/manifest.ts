// ConsolePrefs described as an admin-manifest `settings` group
// (design-kit docs/admin-manifest-contract.md, contract_version 1), so kit-settings'
// <SettingsRenderer groups={[group]} states={...}/> can render and edit it.
// Pure data + projections; no transport. The group is flat scalars only (the v1
// profile), so list prefs (mutedSources) travel as a comma-separated string.

import { SEVERITY_ORDER } from '../events/types'
import { sanitizePrefs } from './sanitize'
import type { ConsolePrefs } from './types'

export const CONSOLE_PREFS_GROUP_ID = 'console.alerts'
export const ADMIN_CONTRACT_VERSION = 1

export type PrefScalar = string | number | boolean

/** Local mirror of the manifest settings group (only the parts this group uses). */
export interface AdminSettingsGroup {
  id: string
  label: string
  section: 'settings'
  scope: { kind: 'app' | 'plugin' | 'project' | 'user'; id: string }
  schema: {
    $schema: 'https://json-schema.org/draft/2020-12/schema'
    type: 'object'
    additionalProperties: false
    properties: Record<string, Record<string, unknown>>
  }
  fields: Record<string, { editable: boolean; secret: boolean; restart_required: boolean; apply_target?: string; read_only_reason?: string }>
  resolution: { precedence: ('override' | 'env' | 'file' | 'default')[]; write_layer?: 'override' }
  capabilities: { can_read: boolean; can_validate: boolean; can_update: boolean; can_reset: boolean }
  endpoints: Partial<Record<'read' | 'validate' | 'update' | 'reset', { method: 'GET' | 'POST'; path: string }>>
}

/** manifest field key (snake_case) -> ConsolePrefs key */
export const PREF_FIELD_KEYS = {
  quiet: 'quiet',
  toast_min_severity: 'toastMinSeverity',
  quiet_hours_enabled: 'quietHoursEnabled',
  quiet_hours_start: 'quietHoursStart',
  quiet_hours_end: 'quietHoursEnd',
  muted_sources: 'mutedSources',
  coalesce_window_sec: 'coalesceWindowSec',
} as const satisfies Record<string, keyof ConsolePrefs>

const HHMM = '^([01][0-9]|2[0-3]):[0-5][0-9]$'

/**
 * The group a server would publish for the signed-in user. `endpoints` are the
 * proposed paths (BACKEND_NEEDS.md); with the mock store they are never called.
 */
export function consolePrefsSettingsGroup(userId = 'me'): AdminSettingsGroup {
  const base = `/admin/settings/${CONSOLE_PREFS_GROUP_ID}`
  const f = { editable: true, secret: false, restart_required: false }
  return {
    id: CONSOLE_PREFS_GROUP_ID,
    label: 'Console alerts',
    section: 'settings',
    scope: { kind: 'user', id: userId },
    schema: {
      $schema: 'https://json-schema.org/draft/2020-12/schema',
      type: 'object',
      additionalProperties: false,
      properties: {
        quiet: { type: 'boolean', title: 'Quiet mode', description: 'No toasts while on. Critical alerts still break through; the tray and badge keep counting.', default: false },
        toast_min_severity: { type: 'string', title: 'Toast threshold', description: 'Lowest severity that raises a toast.', enum: SEVERITY_ORDER.slice(1), default: 'notice' },
        quiet_hours_enabled: { type: 'boolean', title: 'Quiet hours', description: 'Turn quiet mode on automatically on a daily schedule (your local time).', default: false },
        quiet_hours_start: { type: 'string', title: 'Quiet hours start', description: 'HH:MM, 24h.', pattern: HHMM, minLength: 5, maxLength: 5, default: '22:00' },
        quiet_hours_end: { type: 'string', title: 'Quiet hours end', description: 'HH:MM, 24h. May be earlier than the start (wraps midnight).', pattern: HHMM, minLength: 5, maxLength: 5, default: '07:00' },
        muted_sources: { type: 'string', title: 'Muted sources', description: 'Comma-separated agent or machine names. Muted sources raise no toast; critical alerts still do.', maxLength: 500, default: '' },
        coalesce_window_sec: { type: 'integer', title: 'Coalesce window (seconds)', description: 'Repeated alerts with the same key inside this window fold into one. 0 turns coalescing off.', minimum: 0, maximum: 3600, default: 120 },
      },
    },
    fields: Object.fromEntries(Object.keys(PREF_FIELD_KEYS).map((k) => [k, { ...f }])),
    resolution: { precedence: ['override', 'default'], write_layer: 'override' },
    capabilities: { can_read: true, can_validate: true, can_update: true, can_reset: true },
    endpoints: {
      read: { method: 'GET', path: base },
      validate: { method: 'POST', path: `${base}/validate` },
      update: { method: 'POST', path: `${base}/update` },
      reset: { method: 'POST', path: `${base}/reset` },
    },
  }
}

export interface PrefValueSnapshot {
  present: boolean
  value?: PrefScalar
  editable: boolean
  has_override: boolean
}

/** ConsolePrefs -> kit-settings `SettingsState.values` (every field present; the mock treats all as overridden). */
export function prefsToSnapshotValues(prefs: ConsolePrefs): Record<string, PrefValueSnapshot> {
  const out: Record<string, PrefValueSnapshot> = {}
  for (const [field, key] of Object.entries(PREF_FIELD_KEYS)) {
    const v = prefs[key]
    out[field] = { present: true, value: Array.isArray(v) ? v.join(', ') : v, editable: true, has_override: true }
  }
  return out
}

/** kit-settings `SettingsChanges` ({set, unset}) -> a ConsolePrefs patch. `unset` returns the key to its default. */
export function changesToPrefsPatch(changes: { set: Readonly<Record<string, PrefScalar>>; unset: readonly string[] }, defaults: ConsolePrefs): Partial<ConsolePrefs> {
  const patch: Record<string, unknown> = {}
  const keys = PREF_FIELD_KEYS as Record<string, keyof ConsolePrefs>
  for (const [field, value] of Object.entries(changes.set)) {
    const key = keys[field]
    if (!key) continue
    patch[key] = key === 'mutedSources' ? String(value).split(',') : value
  }
  for (const field of changes.unset) {
    const key = keys[field]
    if (key) patch[key] = defaults[key]
  }
  const clean = sanitizePrefs({ ...defaults, ...patch })
  return Object.fromEntries(Object.keys(patch).map((k) => [k, clean[k as keyof ConsolePrefs]])) as Partial<ConsolePrefs>
}
