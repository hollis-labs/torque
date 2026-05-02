import type { Task } from '@/lib/types'

export interface ScopeTaskRollup {
  total: number
  done: number
  open: number
  doing: number
  review: number
  blocked: number
  completion: number
}

export function buildTaskRollup(tasks: Task[]): ScopeTaskRollup {
  const total = tasks.length
  const done = tasks.filter((task) => task.status === 'done').length
  const open = tasks.filter((task) => ['backlog', 'todo', 'queued'].includes(task.status)).length
  const doing = tasks.filter((task) => task.status === 'doing').length
  const review = tasks.filter((task) => task.status === 'review').length
  const blocked = tasks.filter((task) => task.status === 'blocked').length

  return {
    total,
    done,
    open,
    doing,
    review,
    blocked,
    completion: total > 0 ? Math.round((done / total) * 100) : 0,
  }
}
