import type { TaskStatus } from './types'

export const TASK_STATUSES: readonly TaskStatus[] = [
  'backlog',
  'todo',
  'queued',
  'doing',
  'review',
  'done',
  'blocked',
  'paused',
  'archived',
] as const

export const PRIORITIES = [
  { value: 1, label: 'P1', description: 'Critical' },
  { value: 2, label: 'P2', description: 'Default' },
  { value: 3, label: 'P3', description: 'Low' },
] as const

export const MODE_PRESETS = {
  planning: ['backlog', 'todo'] as TaskStatus[],
  executing: ['queued', 'doing', 'blocked', 'paused', 'review'] as TaskStatus[],
  reviewing: ['done', 'review'] as TaskStatus[],
} as const

export const DEFAULT_ACTIVE_STATUSES: TaskStatus[] = TASK_STATUSES.filter(
  (s) => s !== 'archived'
)

/** Maps each TaskStatus to its CSS variable token name (legacy — kept for compat) */
export const STATUS_COLOR_VAR: Record<TaskStatus, string> = {
  backlog: 'var(--color-status-backlog)',
  todo: 'var(--color-status-todo)',
  queued: 'var(--color-status-queued)',
  doing: 'var(--color-status-doing)',
  review: 'var(--color-status-review)',
  done: 'var(--color-status-done)',
  blocked: 'var(--color-status-blocked)',
  paused: 'var(--color-status-paused)',
  archived: 'var(--color-status-archived)',
}

/** Maps each TaskStatus to a human-readable label */
export const STATUS_LABEL: Record<TaskStatus, string> = {
  backlog: 'Backlog',
  todo: 'Todo',
  queued: 'Queued',
  doing: 'Doing',
  review: 'Review',
  done: 'Done',
  blocked: 'Blocked',
  paused: 'Paused',
  archived: 'Archived',
}

/** Per-status Tailwind color classes (bg, text, border, dot) */
export const STATUS_COLORS: Record<string, { bg: string; text: string; border: string; dot: string }> = {
  backlog:  { bg: 'bg-slate-500/10',   text: 'text-slate-300',   border: 'border-slate-500/40',   dot: 'bg-slate-400'   },
  todo:     { bg: 'bg-zinc-500/10',    text: 'text-zinc-300',    border: 'border-zinc-500/40',    dot: 'bg-zinc-400'    },
  queued:   { bg: 'bg-amber-500/10',   text: 'text-amber-200',   border: 'border-amber-500/40',   dot: 'bg-amber-300'   },
  doing:    { bg: 'bg-blue-500/10',    text: 'text-blue-200',    border: 'border-blue-500/40',    dot: 'bg-blue-300'    },
  review:   { bg: 'bg-purple-500/10',  text: 'text-purple-200',  border: 'border-purple-500/40',  dot: 'bg-purple-300'  },
  done:     { bg: 'bg-emerald-500/10', text: 'text-emerald-200', border: 'border-emerald-500/40', dot: 'bg-emerald-300' },
  blocked:  { bg: 'bg-red-500/10',     text: 'text-red-200',     border: 'border-red-500/40',     dot: 'bg-red-300'     },
  paused:   { bg: 'bg-yellow-500/10',  text: 'text-yellow-200',  border: 'border-yellow-500/40',  dot: 'bg-yellow-300'  },
  archived: { bg: 'bg-zinc-600/10',    text: 'text-zinc-400',    border: 'border-zinc-600/40',    dot: 'bg-zinc-600'    },
}

export const DEFAULT_STATUS_COLOR = {
  bg: 'bg-zinc-500/10',
  text: 'text-zinc-300',
  border: 'border-zinc-500/40',
  dot: 'bg-zinc-400',
}
