export type TaskStatus =
  | 'backlog'
  | 'todo'
  | 'queued'
  | 'doing'
  | 'review'
  | 'done'
  | 'blocked'
  | 'paused'
  | 'archived'

export type TagColor =
  | 'zinc' | 'red' | 'orange' | 'amber'
  | 'green' | 'teal' | 'blue' | 'violet' | 'pink'

export interface Tag {
  slug: string
  name: string
  description: string
  color: TagColor
  created_at: string
  updated_at: string
}

export type OnDone = 'close' | 'review' | 'notify'
export type OnFail = 'retry' | 'block' | 'escalate' | 'notify'
export type OnReview = 'pause' | 'notify' | 'auto-approve'
export type OnDoneMerge = 'none' | 'auto' | 'pr' | 'auto-resolve'

export type DeliverableType =
  | 'diff'
  | 'test-results'
  | 'screenshot'
  | 'pr-link'
  | 'branch'
  | 'commit'
  | 'log'
  | 'finding'
  | 'report'
  | 'note'
  | 'metrics'
  | 'custom'

export interface Deliverable {
  type: DeliverableType
  required: boolean
  description?: string
}

/**
 * Sentinel value meaning "unlimited — no cap" for the three nullable
 * numeric task fields (cost_budget, max_duration_ms, token_budget).
 */
export const UNLIMITED = -1 as const

export interface Task {
  id: string
  title: string
  description: string
  status: TaskStatus
  priority: number
  tags: Tag[]
  manual: boolean
  executor: string
  agent_profile: string
  working_dir: string

  // Execution context
  tools: string[]
  permissions: Record<string, unknown>
  environment: Record<string, string>
  system_prompt: string
  files: string[]

  // Budget & limits
  /**
   * Cost budget in dollars. Sentinel values:
   * - `null` — use default (inherit from sprint/global)
   * - `-1` — unlimited (no cap)
   * - `0` — explicit zero (no spend allowed)
   * - positive — specific budget
   */
  cost_budget: number | null
  max_retries: number
  /**
   * Max execution duration in milliseconds. Sentinel values:
   * - `null` — use default
   * - `-1` — unlimited
   * - positive — specific duration
   */
  max_duration_ms: number | null
  /**
   * Max tokens per run. Sentinel values:
   * - `null` — use default
   * - `-1` — unlimited
   * - positive — specific cap
   */
  token_budget: number | null

  // Lifecycle rules (narrowed from string to enum unions)
  on_done: OnDone
  on_fail: OnFail
  on_review: OnReview
  on_done_merge: OnDoneMerge

  // Lifecycle extensions
  escalation_chain: string[]
  quality_gates: string[]

  // Deliverables
  deliverables: Deliverable[]
  deliverable_preset: string

  // Dependencies
  depends_on: string[]
  blocked_reason: string

  // Metadata
  metadata: Record<string, unknown>

  // Grouping
  sprint_id: string | null
  project_id: string | null
  epic_id: string | null

  // Audit
  created_at: string
  updated_at: string
}

export interface Run {
  id: number
  task_id: string
  executor: string
  agent_profile: string
  status: string
  prompt_tokens: number
  completion_tokens: number
  cost: number
  exit_code: number
  error_message: string
  started_at: string
  completed_at: string | null
}

export interface Artifact {
  id: number
  task_id: string
  run_id: number
  type: string
  content: string
  url: string
  file_path: string
  created_at: string
}

export interface Comment {
  id: number
  task_id: string
  author: string
  content: string
  created_at: string
}

export interface SSEEvent {
  type: string
  data: Record<string, unknown>
}

export interface FeatureFlags {
  sprints: boolean
  projects: boolean
  epics: boolean
}

export type ContainerStatus = 'active' | 'inactive'

export interface Project {
  id: string
  name: string
  description: string
  repo_path: string
  status: ContainerStatus
  icon: string
  created_at: string
  updated_at: string
}

export interface Sprint {
  id: string
  name: string
  status: ContainerStatus
  approval_mode: string
  cost_budget: number | null
  project_id: string | null
  started_at: string | null
  ended_at: string | null
  created_at: string
  updated_at: string
}

export interface Epic {
  id: string
  name: string
  description: string
  status: ContainerStatus
  priority: number | null
  project_id: string | null
  created_at: string
  updated_at: string
}

export interface TaskFilter {
  status?: TaskStatus[]
  priority?: number[]
  tags?: string[]
  sprint_id?: string
  project_id?: string
  epic_id?: string
  search?: string
  limit?: number
  offset?: number
}
