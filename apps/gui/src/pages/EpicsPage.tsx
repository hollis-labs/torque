import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ExternalLink, Pencil, Plus } from 'lucide-react'
import { Skeleton, Button, PageHeader, SummaryCards, EmptyState } from '@hollis-labs/sysop-ui'
import { EpicCreateDialog } from '@/components/domain/epic-create-dialog'
import { ScopeOverviewCard } from '@/components/domain/scope-overview-card'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { buildTaskRollup, groupTasksByScope } from '@/lib/scope-metrics'
import type { Epic, Project, Task } from '@/lib/types'

const SSE_EVENTS = ['epic.updated', 'epic.created', 'epic.deleted', 'task.updated', 'task.created', 'task.transitioned']

function PageSkeleton() {
  return (
    <div className="flex flex-col gap-4 p-6">
      <Skeleton className="h-40 w-full rounded-2xl" />
      <Skeleton className="h-40 w-full rounded-2xl" />
    </div>
  )
}

export default function EpicsPage() {
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)
  const [epics, setEpics] = useState<Epic[]>([])
  const [projects, setProjects] = useState<Project[]>([])
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const loadGeneration = useRef(0)

  const load = useCallback(async () => {
    const myGen = ++loadGeneration.current
    setLoading(true)
    setError(null)
    try {
      const [epicRes, projectRes, taskRes] = await Promise.all([
        api.listEpics(),
        api.listProjects(),
        api.listTasks(),
      ])
      if (myGen !== loadGeneration.current) return
      setEpics(epicRes.epics)
      setProjects(projectRes.projects)
      setTasks(taskRes.tasks)
    } catch (err) {
      if (myGen !== loadGeneration.current) return
      setError(err instanceof Error ? err.message : 'Failed to load epics')
    } finally {
      if (myGen === loadGeneration.current) setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (!lastEvent) return
    void load()
  }, [lastEvent, load])

  const epicTasks = useMemo(() => groupTasksByScope(tasks, 'epic_id'), [tasks])
  const projectNames = useMemo(
    () => new Map(projects.map((project) => [project.id, project.name])),
    [projects]
  )

  const summaryCards = [
    { label: 'Epics', value: epics.length },
    { label: 'Active', value: epics.filter((epic) => epic.status === 'active').length, accentColor: '#34d399' },
    { label: 'Scoped Tasks', value: tasks.filter((task) => task.epic_id).length, accentColor: '#a78bfa' },
    { label: 'P1', value: epics.filter((epic) => epic.priority === 1).length, accentColor: '#f87171' },
  ]

  const epicCards = useMemo(() => {
    return epics.map((epic) => {
      return { epic, rollup: buildTaskRollup(epicTasks.get(epic.id) ?? []) }
    })
  }, [epics, epicTasks])

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Epics">
        <Button variant="outline" size="sm" onClick={() => navigate('/operations')}>
          <ExternalLink className="h-3.5 w-3.5" />
          Operations
        </Button>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          New Epic
        </Button>
      </PageHeader>
      {!loading && !error && <SummaryCards cards={summaryCards} />}

      <EpicCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        projects={projects}
        onCreated={(epic) => navigate(`/epics/${epic.id}`)}
      />

      <div className="flex-1 overflow-auto">
        {loading ? (
          <PageSkeleton />
        ) : error ? (
          <div className="p-6">
            <EmptyState variant="error" title="Something went wrong" description={error} action={{ label: 'Retry', onClick: load }} />
          </div>
        ) : epicCards.length === 0 ? (
          <div className="p-6">
            <EmptyState
              variant="empty"
              title="No epics yet"
              description="No epics exist yet."
              action={{ label: 'Create epic', onClick: () => setCreateOpen(true) }}
            />
          </div>
        ) : (
          <div className="flex flex-col gap-4 p-6">
            {epicCards.map(({ epic, rollup }) => (
              <ScopeOverviewCard
                key={epic.id}
                kindLabel="Epic"
                title={epic.name}
                to={`/epics/${epic.id}`}
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
                  { label: 'Project', value: epic.project_id ? (projectNames.get(epic.project_id) ?? epic.project_id) : 'None' },
                  { label: 'Priority', value: epic.priority === null ? 'None' : `P${epic.priority}` },
                  { label: 'Updated', value: new Date(epic.updated_at).toLocaleDateString() },
                ]}
                actions={
                  <>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/epics/${epic.id}/edit`)}>
                      <Pencil className="h-3.5 w-3.5" />
                      Edit
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/operations?epic_id=${epic.id}`)}>
                      <ExternalLink className="h-3.5 w-3.5" />
                      Tasks
                    </Button>
                  </>
                }
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
