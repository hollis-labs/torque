import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ExternalLink, Pencil } from 'lucide-react'
import { Skeleton, Button, DetailHeader, EmptyState } from '@hollis-labs/sysop-ui'
import { ScopeDetailHero } from '@/components/domain/scope-detail-hero'
import { ScopeMetaCard } from '@/components/domain/scope-meta-card'
import { ScopeTaskPanel } from '@/components/domain/scope-task-panel'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { buildTaskRollup } from '@/lib/scope-metrics'
import type { Epic, Project, Task } from '@/lib/types'

const SSE_EVENTS = ['epic.updated', 'epic.created', 'epic.deleted', 'task.updated', 'task.created', 'task.transitioned']

export default function EpicDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)
  const [epic, setEpic] = useState<Epic | null>(null)
  const [project, setProject] = useState<Project | null>(null)
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const loadGeneration = useRef(0)

  const load = useCallback(async () => {
    if (!id) return
    const myGen = ++loadGeneration.current
    setLoading(true)
    setError(null)
    try {
      const [nextEpic, taskRes] = await Promise.all([
        api.getEpic(id),
        api.listTasks({ epic_id: id }),
      ])
      if (myGen !== loadGeneration.current) return
      const nextProject = nextEpic.project_id
        ? await api.getProject(nextEpic.project_id).catch(() => null)
        : null
      if (myGen !== loadGeneration.current) return
      setEpic(nextEpic)
      setTasks(taskRes.tasks)
      setProject(nextProject)
    } catch (err) {
      if (myGen !== loadGeneration.current) return
      setError(err instanceof Error ? err.message : 'Failed to load epic')
    } finally {
      if (myGen === loadGeneration.current) setLoading(false)
    }
  }, [api, id])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (!lastEvent) return
    void load()
  }, [lastEvent, load])

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

  if (error || !epic) {
    return (
      <div className="p-6">
        <EmptyState variant="error" title="Something went wrong" description={error ?? 'Epic not found.'} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <DetailHeader title={epic.name} backHref="/epics" backLabel="Epics" id={epic.id} status={epic.status} />
      <div className="flex-1 overflow-auto p-6">
        <div className="flex flex-col gap-6">
          <ScopeDetailHero
            kindLabel="Epic"
            title={epic.name}
            id={epic.id}
            status={epic.status}
            description={epic.description}
            progress={rollup}
            metrics={[
              { label: 'Open', value: rollup.open },
              { label: 'Doing', value: rollup.doing, accentColor: '#60a5fa' },
              { label: 'Blocked', value: rollup.blocked, accentColor: '#f87171' },
              { label: 'Done', value: rollup.done, accentColor: '#34d399' },
            ]}
            meta={[
              { label: 'Project', value: project?.name ?? 'None' },
              { label: 'Priority', value: epic.priority === null ? 'None' : `P${epic.priority}` },
              { label: 'Updated', value: new Date(epic.updated_at).toLocaleString() },
            ]}
            actions={
              <>
                <Button variant="outline" size="sm" onClick={() => navigate(`/epics/${epic.id}/edit`)}>
                  <Pencil className="h-3.5 w-3.5" />
                  Edit
                </Button>
                <Button variant="outline" size="sm" onClick={() => navigate(`/operations?epic_id=${epic.id}`)}>
                  <ExternalLink className="h-3.5 w-3.5" />
                  Open in Operations
                </Button>
              </>
            }
          />

          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <ScopeMetaCard label="Description" value={epic.description || 'No description set.'} className="md:col-span-2 xl:col-span-4" />
          </div>

          <ScopeTaskPanel tasks={tasks} />
        </div>
      </div>
    </div>
  )
}
