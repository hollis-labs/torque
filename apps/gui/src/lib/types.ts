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

export interface Task {
  id: string
  title: string
  description: string
  status: TaskStatus
  priority: number
  tags: string
  manual: boolean
  executor: string
  agent_profile: string
  working_dir: string
  system_prompt: string
  cost_budget: number | null
  max_retries: number
  on_done: string
  on_fail: string
  on_review: string
  on_done_merge: string
  blocked_reason: string
  sprint_id: string | null
  project_id: string | null
  epic_id: string | null
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
