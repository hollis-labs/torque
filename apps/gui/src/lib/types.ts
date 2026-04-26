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

export type TaskKind = 'agent' | 'external' | 'wait' | 'decision' | 'parent' | 'plan'
export type TaskSourceType = 'agent' | 'user' | 'api' | 'system' | 'webhook' | 'import'
export type TaskTrust = 'trusted' | 'normal' | 'untrusted'
export type TaskCheckpointMode = 'none' | 'blocking' | 'non_blocking'
export type TaskOnCheckpointResponse = 'resume' | 'review' | 'custom'

export interface TemplateRef {
  id: string
  version: number
}

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

  // Facets (migration 007)
  kind: TaskKind
  source_type: TaskSourceType
  source_ref: string | null
  trust: TaskTrust
  checkpoint_mode: TaskCheckpointMode
  on_checkpoint_response: TaskOnCheckpointResponse

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

  // Parent linkage (migration 013). null = top of lineage.
  parent_id?: string | null

  // Audit
  created_at: string
  updated_at: string

  /**
   * Run roll-up — sum of prompt/completion tokens and cost across every
   * recorded run for this task, plus a turn count. Always present on
   * task responses; zero-valued for tasks that have never executed.
   */
  stats?: TaskStats

  /**
   * Structural checklist. Populated at create time by extracting top-level
   * `- [ ]` / `- [x]` markdown from the description, writable thereafter via
   * MCP or the subtodos HTTP endpoints. Always an array on task responses;
   * empty when the task has no checklist.
   */
  subtodos?: Subtodo[]
}

export interface TaskStats {
  run_count: number
  prompt_tokens: number
  completion_tokens: number
  cost: number
}

export interface Subtodo {
  id: string
  text: string
  required: boolean
  done: boolean
  evidence?: string
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
  run_id: number | null
  type: string
  content: string
  url: string
  file_path: string
  /**
   * Free-form record written by the creating agent/user. The `origin` key,
   * if present, drives the agent/user/system badge; other keys are surfaced
   * verbatim in the collapsed details panel.
   */
  metadata?: Record<string, unknown>
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
  /** Narrow to a single task-kind. "plan" is used by the Plans GUI. */
  kind?: TaskKind
  /**
   * Parent linkage filter (migration 013). "null" returns roots
   * (parent_id IS NULL); any other value matches that parent id.
   */
  parent_id?: string | 'null'
  /** Manual-flag filter. `true` = manual-hold only, `false` = auto-eligible only. */
  manual?: boolean
}

/**
 * Plan types — kind=plan tasks coordinate phase-scoped child tasks via
 * parent_id + metadata.phase_id. See docs/plans-v1.md.
 */
export interface PlanPhase {
  id: string
  name: string
  order: number
  acceptance?: string
}

export interface PlanMetadata {
  version: number
  phases: PlanPhase[]
}

export interface PlanPhaseInput {
  name: string
  acceptance?: string
}

export interface PhaseRollup {
  total: number
  done: number
  blocked: number
}

export interface PlanProgress {
  total_children: number
  done: number
  blocked: number
  by_phase: Record<string, PhaseRollup>
}

export interface PlanDetail {
  task: Task
  plan: PlanMetadata
  progress: PlanProgress
}

export interface Template {
  id: string
  version: number
  name: string
  description: string
  kind: string
  auto_execute: boolean
  executor: string | null
  agent_profile: string | null
  system_prompt: string | null
  working_dir: string | null
  tools: string[]
  permissions: Record<string, unknown>
  environment: Record<string, string>
  cost_budget: number | null
  max_retries: number
  max_duration_ms: number | null
  token_budget: number | null
  on_done: OnDone
  on_fail: OnFail
  on_review: OnReview
  on_done_merge: OnDoneMerge
  escalation_chain: string[]
  quality_gates: string[]
  deliverables: Deliverable[]
  checkpoint_mode: TaskCheckpointMode
  on_checkpoint_response: TaskOnCheckpointResponse
  metadata_template: Record<string, unknown>
  required_vars: string[]
  tags: string[]
  is_archived: boolean
  created_at: string
  updated_at: string
}

export interface TemplateInstantiateRequest {
  template_version?: number
  title: string
  description?: string
  vars?: Record<string, string>
  overrides?: Record<string, unknown>
  sprint_id?: string
  project_id?: string
  epic_id?: string
  tags?: string[]
}

export interface ModelPricing {
  input: number
  output: number
  cache_write?: number
  cache_read?: number
  reasoning?: number
}

export interface ModelLimits {
  context_window: number
  max_output_tokens: number
}

export interface ModelModality {
  input: string[]
  output: string[]
}

export interface ModelCapabilities {
  tool_call: boolean
  reasoning: boolean
  attachment: boolean
  temperature: boolean
}

/**
 * Mirrors httpserver.modelEntry — flat ModelRef with snake_case JSON keys.
 * Returned by GET /api/v1/models (list) and GET /api/v1/models/{provider}/{model} (get).
 */
export interface ModelEntry {
  provider_id: string
  id: string
  name: string
  family: string
  open_weights: boolean
  release_date: string
  knowledge_cutoff: string
  last_updated: string
  cost: ModelPricing
  limit: ModelLimits
  modality: ModelModality
  capabilities: ModelCapabilities
}

export interface SchedulerStatus {
  enabled: boolean
  max_workers: number
  active_workers: number
  queue_depth: number
  total_cost: number
  subscribers: number
  stale_heartbeat_threshold_seconds: number
}

export type CheckpointStatus = 'pending' | 'responded' | 'canceled' | 'timed_out'

export interface Checkpoint {
  id: number
  task_id: string
  run_id: number | null
  correlation_id: string
  type: string
  payload_json: string
  response_json: string | null
  emitter_source_type: string
  emitter_source_ref: string | null
  responder_source_type: string | null
  responder_source_ref: string | null
  emitted_at: string
  responded_at: string | null
  timeout_at: string | null
  status: CheckpointStatus
}
