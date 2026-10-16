import { createTorqueTaskResolver, type KindResolver } from '../refs'
import type { TorqueApiClient } from '@/lib/api'

/**
 * Resolvers that talk to the real backend today: only `torque.task`, through
 * the existing `/planner/torque-titles` batch endpoint. Every other kind stays
 * unresolved (chip + link, no preview) until its endpoint exists; see BACKEND_NEEDS.md.
 * Memoise the result before handing it to AutolinkProvider.
 */
export function createLiveResolvers(api: Pick<TorqueApiClient, 'plannerTaskTitles'>): Record<string, KindResolver> {
  return { 'torque.task': createTorqueTaskResolver((ids, signal) => api.plannerTaskTitles(ids, signal)) }
}
