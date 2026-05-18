import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ExternalLink, Pencil, Plus } from 'lucide-react'
import { Skeleton, Button, PageHeader, SummaryCards, EmptyState } from '@hollis-labs/sysop-ui'
import { SprintCreateDialog } from '@/components/domain/sprint-create-dialog'
import { ScopeOverviewCard } from '@/components/domain/scope-overview-card'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { buildTaskRollup, groupTasksByScope } from '@/lib/scope-metrics'
import type { Project, Sprint, Task } from '@/lib/types'

const SSE_EVENTS = ['sprint.updated', 'sprint.created', 'sprint.deleted', 'task.updated', 'task.created', 'task.transitioned']

function PageSkeleton() {
  return (
    <div className="flex flex-col gap-4 p-6">
      <Skeleton className="h-40 w-full rounded-2xl" />
      <Skeleton className="h-40 w-full rounded-2xl" />
    </div>
  )
}

export default function SprintsPage() {
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)
  const [sprints, setSprints] = useState<Sprint[]>([])
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
      const [sprintRes, projectRes, taskRes] = await Promise.all([
        api.listSprints(),
        api.listProjects(),
        api.listTasks(),
      ])
      if (myGen !== loadGeneration.current) return
      setSprints(sprintRes.sprints)
      setProjects(projectRes.projects)
      setTasks(taskRes.tasks)
    } catch (err) {
      if (myGen !== loadGeneration.current) return
      setError(err instanceof Error ? err.message : 'Failed to load sprints')
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

  const sprintTasks = useMemo(() => groupTasksByScope(tasks, 'sprint_id'), [tasks])
  const projectNames = useMemo(
    () => new Map(projects.map((project) => [project.id, project.name])),
    [projects]
  )

  const summaryCards = [
    { label: 'Sprints', value: sprints.length },
    { label: 'Active', value: sprints.filter((sprint) => sprint.status === 'active').length, accentColor: '#34d399' },
    { label: 'Completed', value: sprints.filter((sprint) => sprint.status === 'completed').length, accentColor: '#60a5fa' },
    { label: 'Scoped Tasks', value: tasks.filter((task) => task.sprint_id).length, accentColor: '#fbbf24' },
  ]

  const sprintCards = useMemo(() => {
    return sprints.map((sprint) => {
      return { sprint, rollup: buildTaskRollup(sprintTasks.get(sprint.id) ?? []) }
    })
  }, [sprints, sprintTasks])

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Sprints">
        <Button variant="outline" size="sm" onClick={() => navigate('/operations')}>
          <ExternalLink className="h-3.5 w-3.5" />
          Operations
        </Button>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          New Sprint
        </Button>
      </PageHeader>
      {!loading && !error && <SummaryCards cards={summaryCards} />}

      <SprintCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        projects={projects}
        onCreated={(sprint) => navigate(`/sprints/${sprint.id}`)}
      />

      <div className="flex-1 overflow-auto">
        {loading ? (
          <PageSkeleton />
        ) : error ? (
          <div className="p-6">
            <EmptyState variant="error" title="Something went wrong" description={error} action={{ label: 'Retry', onClick: load }} />
          </div>
        ) : sprintCards.length === 0 ? (
          <div className="p-6">
            <EmptyState
              variant="empty"
              title="No sprints yet"
              description="No sprints exist yet."
              action={{ label: 'Create sprint', onClick: () => setCreateOpen(true) }}
            />
          </div>
        ) : (
          <div className="flex flex-col gap-4 p-6">
            {sprintCards.map(({ sprint, rollup }) => (
              <ScopeOverviewCard
                key={sprint.id}
                kindLabel="Sprint"
                title={sprint.name}
                to={`/sprints/${sprint.id}`}
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
                  { label: 'Project', value: sprint.project_id ? (projectNames.get(sprint.project_id) ?? sprint.project_id) : 'None' },
                  { label: 'Approval', value: sprint.approval_mode || 'approve_each' },
                  { label: 'Budget', value: sprint.cost_budget === null ? 'None' : `$${sprint.cost_budget.toFixed(2)}` },
                  { label: 'Updated', value: new Date(sprint.updated_at).toLocaleDateString() },
                ]}
                actions={
                  <>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/sprints/${sprint.id}/edit`)}>
                      <Pencil className="h-3.5 w-3.5" />
                      Edit
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/operations?sprint_id=${sprint.id}`)}>
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
