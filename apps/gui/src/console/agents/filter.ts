import type { AgentListQuery, AgentRecord } from './types'

const LIVE_ORDER = { waiting: 0, busy: 1, idle: 2, offline: 3 } as const

/** Pure filter + sort shared by the mock client and the view. */
export function filterAgents(agents: AgentRecord[], q: AgentListQuery = {}): AgentRecord[] {
  const needle = q.search?.trim().toLowerCase()
  return agents
    .filter((a) => {
      if (q.provider && a.runtime.provider !== q.provider) return false
      if (q.role && !a.identity.roles.includes(q.role)) return false
      if (!needle) return true
      const hay = [a.identity.id, a.identity.name, a.identity.summary ?? '', a.runtime.provider, a.runtime.project ?? '', ...a.identity.roles, ...a.skills].join(' ').toLowerCase()
      return hay.includes(needle)
    })
    .sort((a, b) => {
      const la = a.live ? LIVE_ORDER[a.live.state] : 4
      const lb = b.live ? LIVE_ORDER[b.live.state] : 4
      return la - lb || a.identity.name.localeCompare(b.identity.name)
    })
}

/** Plain-language one-liner for the permissions column. */
export function policySummary(a: AgentRecord): string {
  const p = a.policy
  const parts = [p.permissionMode === 'bypass' ? 'bypass permissions' : 'asks before risky tools']
  if (p.sandbox === 'unrestricted') parts.push('unrestricted sandbox')
  if (p.network) parts.push('network on')
  if (p.scopes.length) parts.push(`${p.scopes.length} privileged scope${p.scopes.length === 1 ? '' : 's'}`)
  return parts.join(' · ')
}

export function isPrivileged(a: AgentRecord): boolean {
  return a.policy.permissionMode === 'bypass' || a.policy.sandbox === 'unrestricted' || a.policy.scopes.length > 0
}
