import { createScopedStorage } from '@hollis-labs/sysop-ui/api'
import { parseOpsFilters, type OpsFilters } from './ops-filters-storage'

export interface OpsSavedView { id: string; name: string; filters: OpsFilters }
const views = createScopedStorage<OpsSavedView[]>('torque:ops:views:v1', {
  area: 'local',
  parse(raw: unknown) {
    if (!Array.isArray(raw)) return null
    const seen = new Set<string>()
    return raw.flatMap((entry: unknown) => {
      if (!entry || typeof entry !== 'object') return []
      const value = entry as Record<string, unknown>
      const filters = parseOpsFilters(value.filters)
      if (typeof value.id !== 'string' || !value.id || seen.has(value.id)
        || typeof value.name !== 'string' || !value.name.trim() || !filters) return []
      seen.add(value.id)
      return [{ id: value.id, name: value.name.trim(), filters }]
    })
  },
})
const active = createScopedStorage<string>('torque:ops:active-view:v1', {
  area: 'local', parse: raw => typeof raw === 'string' ? raw : null,
})
export const readOpsViews = () => views.read() ?? []
export const saveOpsViews = (value: OpsSavedView[]) => views.write(value)
export const readActiveOpsView = () => active.read() ?? ''
export const saveActiveOpsView = (id: string) => active.write(id)
