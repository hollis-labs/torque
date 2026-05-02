import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ExternalLink, Pencil } from 'lucide-react'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { DetailHeader } from '@/components/domain/detail-header'
import { EmptyState } from '@/components/domain/empty-state'
import { ScopeDetailHero } from '@/components/domain/scope-detail-hero'
import { ScopeMetaCard } from '@/components/domain/scope-meta-card'
import { ScopeTaskPanel } from '@/components/domain/scope-task-panel'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { buildTaskRollup } from '@/lib/scope-metrics'
import type { Project, Sprint, Task } from '@/lib/types'

const SSE_EVENTS = ['sprint.updated', 'sprint.created', 'sprint.deleted', 'task.updated', 'task.created', 'task.transitioned']

export default function SprintDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)
  const [sprint, setSprint] = useState<Sprint | null>(null)
  const [project, setProject] = useState<Project | null>(null)
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  async function load() {
    if (!id) return
    try {
      const [nextSprint, taskRes] = await Promise.all([
        api.getSprint(id),
        api.listTasks({ sprint_id: id }),
      ])
      setSprint(nextSprint)
      setTasks(taskRes.tasks)
      setProject(nextSprint.project_id ? await api.getProject(nextSprint.project_id).catch(() => null) : null)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load sprint')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [id])

  useEffect(() => {
    if (!lastEvent) return
    void load()
  }, [lastEvent, id])

  const rollup = useMemo(() => buildTaskRollup(tasks), [tasks])

  if (loading) {
    return (
      <div className="p-6 flex flex-col gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-48 w-full rounded-2xl" />
        <Skeleton className="h-64 w-full rounded-2xl" />
      </div>
    )
  }

  if (error || !sprint) {
    return (
      <div className="p-6">
        <EmptyState variant="error" description={error ?? 'Sprint not found.'} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <DetailHeader title={sprint.name} backTo="/sprints" backLabel="Sprints" id={sprint.id} status={sprint.status} />
      <div className="flex-1 overflow-auto p-6">
        <div className="flex flex-col gap-6">
          <ScopeDetailHero
            kindLabel="Sprint"
            title={sprint.name}
            id={sprint.id}
            status={sprint.status}
            description={sprint.goal}
            progress={rollup}
            metrics={[
              { label: 'Open', value: rollup.open },
              { label: 'Doing', value: rollup.doing, accentColor: '#60a5fa' },
              { label: 'Review', value: rollup.review, accentColor: '#a78bfa' },
              { label: 'Done', value: rollup.done, accentColor: '#34d399' },
            ]}
            meta={[
              { label: 'Project', value: project?.name ?? 'None' },
              { label: 'Approval Mode', value: sprint.approval_mode || 'approve_each' },
              { label: 'Budget', value: sprint.cost_budget === null ? 'None' : `$${sprint.cost_budget.toFixed(2)}` },
              { label: 'Started', value: sprint.started_at ? new Date(sprint.started_at).toLocaleDateString() : 'Not set' },
            ]}
            actions={
              <>
                <Button variant="outline" size="sm" onClick={() => navigate(`/sprints/${sprint.id}/edit`)}>
                  <Pencil className="h-3.5 w-3.5" />
                  Edit
                </Button>
                <Button variant="outline" size="sm" onClick={() => navigate(`/operations?sprint_id=${sprint.id}`)}>
                  <ExternalLink className="h-3.5 w-3.5" />
                  Open in Operations
                </Button>
              </>
            }
          />

          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <ScopeMetaCard label="Goal / Description" value={sprint.goal || 'No goal set.'} className="md:col-span-2" />
            <ScopeMetaCard label="Ended" value={sprint.ended_at ? new Date(sprint.ended_at).toLocaleDateString() : 'Not set'} />
            <ScopeMetaCard label="Updated" value={new Date(sprint.updated_at).toLocaleString()} />
          </div>

          <ScopeTaskPanel tasks={tasks} />
        </div>
      </div>
    </div>
  )
}
