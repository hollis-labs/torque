// Agent directory domain types. Shape follows the Tether catalog agent entry
// (tether_catalog_list_agents / tether_agent_show) as best understood today.
// Everything beyond id/name/provider is optional so the real API can land
// field by field. Keep additions additive; see BACKEND_NEEDS.md.

export type PermissionMode = 'default' | 'bypass' | (string & {})
export type SandboxMode = 'workspace' | 'unrestricted' | (string & {})
export type Risk = 'low' | 'medium' | 'high'

export interface AgentIdentity {
  /** Stable catalog slug, e.g. "release-captain". */
  id: string
  name: string
  /** Messaging URN once registered, e.g. msg://agent/agent-mux/agt_x. */
  urn?: string
  roles: string[]
  summary?: string
  enabled: boolean
  /** Retired agents are kept for history but cannot be launched, teamed or assigned (B3). */
  retired?: boolean
  retiredAt?: number | null
}

export interface AgentRuntime {
  /** Provider id, e.g. claude-code. */
  provider: string
  model?: string
  /** Home project; decides which tool servers apply. */
  project?: string
  worker?: string
  workspaceMode?: string
  /** Boot profile id (B2 editor). */
  bootProfile?: string
  /** Appended system prompt text (B2 editor). */
  prompt?: string
}

export interface AgentPolicy {
  permissionMode: PermissionMode
  sandbox?: SandboxMode
  network?: boolean
  /** Privileged Tether scopes, e.g. session.write. */
  scopes: string[]
  rules: string[]
}

export interface AgentTeamMembership {
  teamId: string
  teamName: string
  role: string
}

export interface AgentSource {
  /** Definition file or catalog entry the agent is read from. */
  path: string
  /** Catalog revision or git sha, when known. */
  revision?: string
  updatedAt?: number | null
}

/** Pointer into live Fleet state (the Agents tab). Resolved separately; never required. */
export interface AgentLiveRef {
  state: 'waiting' | 'busy' | 'idle' | 'offline'
  sessions: number
  lastActivity: number | null
}

export interface AgentRecord {
  identity: AgentIdentity
  runtime: AgentRuntime
  policy: AgentPolicy
  /** Tool servers the agent can call, as resolved from its project. */
  tools: string[]
  skills: string[]
  teams: AgentTeamMembership[]
  source: AgentSource
  live?: AgentLiveRef
}

export interface AgentListQuery {
  search?: string
  provider?: string
  role?: string
}
