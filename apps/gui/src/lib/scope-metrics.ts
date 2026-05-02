import type { Task } from '@/lib/types'

export interface ScopeTaskRollup {
  total: number
  done: number
  open: number
  doing: number
  review: number
  blocked: number
  paused: number
  completion: number
}

export function buildTaskRollup(tasks: Task[]): ScopeTaskRollup {
  const done = tasks.filter((task) => task.status === 'done').length
  const open = tasks.filter((task) => ['backlog', 'todo', 'queued'].includes(task.status)).length
  const doing = tasks.filter((task) => task.status === 'doing').length
  const review = tasks.filter((task) => task.status === 'review').length
  const blocked = tasks.filter((task) => task.status === 'blocked').length
  const paused = tasks.filter((task) => task.status === 'paused').length
  const total = open + doing + review + blocked + paused + done

  return {
    total,
    done,
    open,
    doing,
    review,
    blocked,
    paused,
    completion: total > 0 ? Math.round((done / total) * 100) : 0,
  }
}

export function groupTasksByScope(tasks: Task[], key: 'project_id' | 'sprint_id' | 'epic_id'): Map<string, Task[]> {
  const groups = new Map<string, Task[]>()
  for (const task of tasks) {
    const id = task[key]
    if (!id) continue
    const bucket = groups.get(id)
    if (bucket) bucket.push(task)
    else groups.set(id, [task])
  }
  return groups
}
