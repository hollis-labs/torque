import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ExternalLink, Pencil, Plus } from 'lucide-react'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { EmptyState } from '@/components/domain/empty-state'
import { ProjectCreateDialog } from '@/components/domain/project-create-dialog'
import { ScopeOverviewCard } from '@/components/domain/scope-overview-card'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { buildTaskRollup } from '@/lib/scope-metrics'
import type { Epic, Project, Sprint, Task } from '@/lib/types'

const SSE_EVENTS = ['project.updated', 'project.created', 'project.deleted', 'task.updated', 'task.created', 'task.transitioned']

function PageSkeleton() {
  return (
    <div className="flex flex-col gap-4 p-6">
      <Skeleton className="h-40 w-full rounded-2xl" />
      <Skeleton className="h-40 w-full rounded-2xl" />
    </div>
  )
}

export default function ProjectsPage() {
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)
  const [projects, setProjects] = useState<Project[]>([])
  const [tasks, setTasks] = useState<Task[]>([])
  const [sprints, setSprints] = useState<Sprint[]>([])
  const [epics, setEpics] = useState<Epic[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)

  async function load() {
    try {
      const [projectRes, taskRes, sprintRes, epicRes] = await Promise.all([
        api.listProjects(),
        api.listTasks(),
        api.listSprints(),
        api.listEpics(),
      ])
      setProjects(projectRes.projects)
      setTasks(taskRes.tasks)
      setSprints(sprintRes.sprints)
      setEpics(epicRes.epics)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load projects')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  useEffect(() => {
    if (!lastEvent) return
    void load()
  }, [lastEvent])

  const activeCount = projects.filter((project) => project.status === 'active').length
  const scopedTaskCount = tasks.filter((task) => task.project_id).length

  const summaryCards = [
    { label: 'Projects', value: projects.length },
    { label: 'Active', value: activeCount, accentColor: '#34d399' },
    { label: 'Scoped Tasks', value: scopedTaskCount, accentColor: '#60a5fa' },
    { label: 'Sprints', value: sprints.length, accentColor: '#fbbf24' },
    { label: 'Epics', value: epics.length, accentColor: '#a78bfa' },
  ]

  const projectCards = useMemo(() => {
    return projects.map((project) => {
      const projectTasks = tasks.filter((task) => task.project_id === project.id)
      const rollup = buildTaskRollup(projectTasks)
      const projectSprintCount = sprints.filter((sprint) => sprint.project_id === project.id).length
      const projectEpicCount = epics.filter((epic) => epic.project_id === project.id).length
      return { project, rollup, projectSprintCount, projectEpicCount }
    })
  }, [projects, tasks, sprints, epics])

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Projects">
        <Button variant="outline" size="sm" onClick={() => navigate('/operations')}>
          <ExternalLink className="h-3.5 w-3.5" />
          Operations
        </Button>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          New Project
        </Button>
      </PageHeader>
      {!loading && !error && <SummaryCards cards={summaryCards} />}

      <ProjectCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(project) => navigate(`/projects/${project.id}`)}
      />

      <div className="flex-1 overflow-auto">
        {loading ? (
          <PageSkeleton />
        ) : error ? (
          <div className="p-6">
            <EmptyState variant="error" description={error} action={{ label: 'Retry', onClick: load }} />
          </div>
        ) : projectCards.length === 0 ? (
          <div className="p-6">
            <EmptyState
              variant="no-tasks"
              description="No projects exist yet."
              action={{ label: 'Create project', onClick: () => setCreateOpen(true) }}
            />
          </div>
        ) : (
          <div className="flex flex-col gap-4 p-6">
            {projectCards.map(({ project, rollup, projectSprintCount, projectEpicCount }) => (
              <ScopeOverviewCard
                key={project.id}
                kindLabel="Project"
                title={project.name}
                to={`/projects/${project.id}`}
                id={project.id}
                status={project.status}
                description={project.description}
                progress={rollup}
                metrics={[
                  { label: 'Open', value: rollup.open },
                  { label: 'Doing', value: rollup.doing, accentColor: '#60a5fa' },
                  { label: 'Blocked', value: rollup.blocked, accentColor: '#f87171' },
                  { label: 'Done', value: rollup.done, accentColor: '#34d399' },
                ]}
                meta={[
                  { label: 'Repo Path', value: <span className="font-mono text-xs text-zinc-300">{project.repo_path || 'Not set'}</span> },
                  { label: 'Sprints', value: projectSprintCount },
                  { label: 'Epics', value: projectEpicCount },
                  { label: 'Updated', value: new Date(project.updated_at).toLocaleDateString() },
                ]}
                actions={
                  <>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/projects/${project.id}/edit`)}>
                      <Pencil className="h-3.5 w-3.5" />
                      Edit
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/operations?project_id=${project.id}`)}>
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
