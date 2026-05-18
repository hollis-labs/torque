import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ExternalLink, FileText, FolderOpen, Pencil } from 'lucide-react'
import { Skeleton, Button, DetailHeader, EmptyState } from '@hollis-labs/sysop-ui'
import { ScopeDetailHero } from '@/components/domain/scope-detail-hero'
import { ScopeMetaCard } from '@/components/domain/scope-meta-card'
import { ScopeCollectionPanel } from '@/components/domain/scope-collection-panel'
import { ScopeTaskPanel } from '@/components/domain/scope-task-panel'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { isHtmlApiFallbackError } from '@/lib/api'
import { buildTaskRollup, groupTasksByScope } from '@/lib/scope-metrics'
import type { Epic, Project, ProjectArtifact, Sprint, Task } from '@/lib/types'

const SSE_EVENTS = ['project.updated', 'project.created', 'project.deleted', 'task.updated', 'task.created', 'task.transitioned']

function preformatted(value: string[] | Record<string, string> | string) {
  if (Array.isArray(value)) return value.length > 0 ? value.join('\n') : 'None'
  if (typeof value === 'object') {
    const entries = Object.entries(value)
    return entries.length > 0 ? entries.map(([key, val]) => `${key}=${val}`).join('\n') : 'None'
  }
  return value || 'None'
}

export default function ProjectDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)
  const [project, setProject] = useState<Project | null>(null)
  const [tasks, setTasks] = useState<Task[]>([])
  const [sprints, setSprints] = useState<Sprint[]>([])
  const [epics, setEpics] = useState<Epic[]>([])
  const [artifacts, setArtifacts] = useState<ProjectArtifact[]>([])
  const [artifactWarning, setArtifactWarning] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const loadGeneration = useRef(0)

  const load = useCallback(async () => {
    if (!id) return
    const myGen = ++loadGeneration.current
    setLoading(true)
    setError(null)
    try {
      const [nextProject, taskRes, sprintRes, epicRes] = await Promise.all([
        api.getProject(id),
        api.listTasks({ project_id: id }),
        api.listSprints({ project_id: id }),
        api.listEpics({ project_id: id }),
      ])
      if (myGen !== loadGeneration.current) return
      setProject(nextProject)
      setTasks(taskRes.tasks)
      setSprints(sprintRes.sprints)
      setEpics(epicRes.epics)

      try {
        const artifactRes = await api.listProjectArtifacts(id)
        if (myGen !== loadGeneration.current) return
        setArtifacts(artifactRes.artifacts)
        setArtifactWarning(null)
      } catch (err) {
        if (myGen !== loadGeneration.current) return
        if (isHtmlApiFallbackError(err) || (err instanceof Error && /HTTP 404|not found/i.test(err.message))) {
          setArtifacts([])
          setArtifactWarning('Project artifacts are unavailable from the current backend runtime. The API route is missing or stale.')
        } else {
          throw err
        }
      }
    } catch (err) {
      if (myGen !== loadGeneration.current) return
      setError(err instanceof Error ? err.message : 'Failed to load project')
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

  const tasksBySprint = useMemo(() => groupTasksByScope(tasks, 'sprint_id'), [tasks])
  const tasksByEpic = useMemo(() => groupTasksByScope(tasks, 'epic_id'), [tasks])
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

  if (error || !project) {
    return (
      <div className="p-6">
        <EmptyState variant="error" title="Something went wrong" description={error ?? 'Project not found.'} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <DetailHeader title={project.name} backHref="/projects" backLabel="Projects" id={project.id} status={project.status} />
      <div className="flex-1 overflow-auto p-6">
        <div className="flex flex-col gap-6">
          <ScopeDetailHero
            kindLabel="Project"
            title={project.name}
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
              { label: 'Agent Path', value: <span className="font-mono text-xs text-zinc-300">{project.agent_path || 'Not set'}</span> },
              { label: 'Sprints', value: sprints.length },
              { label: 'Epics', value: epics.length },
            ]}
            actions={
              <>
                <Button variant="outline" size="sm" onClick={() => navigate(`/projects/${project.id}/edit`)}>
                  <Pencil className="h-3.5 w-3.5" />
                  Edit
                </Button>
                <Button variant="outline" size="sm" onClick={() => navigate(`/operations?project_id=${project.id}`)}>
                  <ExternalLink className="h-3.5 w-3.5" />
                  Open in Operations
                </Button>
              </>
            }
          />

          <div className="grid gap-6 xl:grid-cols-2">
            <ScopeCollectionPanel
              title="Sprints"
              items={sprints.map((sprint) => ({
                id: sprint.id,
                title: sprint.name,
                to: `/sprints/${sprint.id}`,
                status: sprint.status,
                subtitle: sprint.goal || 'No goal set.',
                progress: buildTaskRollup(tasksBySprint.get(sprint.id) ?? []),
              }))}
              emptyMessage="No sprints are attached to this project yet."
            />
            <ScopeCollectionPanel
              title="Epics"
              items={epics.map((epic) => ({
                id: epic.id,
                title: epic.name,
                to: `/epics/${epic.id}`,
                status: epic.status,
                subtitle: epic.description || 'No description set.',
                progress: buildTaskRollup(tasksByEpic.get(epic.id) ?? []),
              }))}
              emptyMessage="No epics are attached to this project yet."
            />
          </div>

          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <ScopeMetaCard label="Read Paths" value={<pre className="whitespace-pre-wrap break-words text-sm text-zinc-200">{preformatted(project.read_paths)}</pre>} />
            <ScopeMetaCard label="Write Paths" value={<pre className="whitespace-pre-wrap break-words text-sm text-zinc-200">{preformatted(project.write_paths)}</pre>} />
            <ScopeMetaCard label="Context Paths" value={<pre className="whitespace-pre-wrap break-words text-sm text-zinc-200">{preformatted(project.context_paths)}</pre>} />
            <ScopeMetaCard label="Permissions" value={<pre className="whitespace-pre-wrap break-words text-sm text-zinc-200">{preformatted(project.permissions)}</pre>} />
            <ScopeMetaCard label="Rules" value={<pre className="whitespace-pre-wrap break-words text-sm text-zinc-200">{preformatted(project.rules)}</pre>} className="md:col-span-2 xl:col-span-4" />
          </div>

          <section className="rounded-2xl border border-zinc-800/80 bg-zinc-950/70">
            <div className="border-b border-zinc-800/80 px-5 py-4">
              <h2 className="text-sm font-semibold uppercase tracking-[0.18em] text-zinc-400">Artifacts</h2>
              <p className="mt-1 text-sm text-zinc-500">Authoritative project documents and folders inherited by tasks.</p>
            </div>
            {artifactWarning && (
              <div className="border-b border-amber-700/40 bg-amber-950/30 px-5 py-3 text-sm text-amber-200">
                {artifactWarning}
              </div>
            )}
            {artifacts.length === 0 ? (
              <div className="px-5 py-8 text-sm text-zinc-500">No project artifacts registered yet.</div>
            ) : (
              <div className="divide-y divide-zinc-800/70">
                {artifacts.map((artifact) => (
                  <div key={artifact.id} className="grid gap-3 px-5 py-4 xl:grid-cols-[minmax(0,1fr)_220px_220px]">
                    <div className="min-w-0">
                      <div className="flex items-center gap-2 text-zinc-100">
                        {artifact.entry_type === 'folder' ? <FolderOpen className="h-4 w-4" /> : <FileText className="h-4 w-4" />}
                        <span className="font-medium">{artifact.title || artifact.file_path}</span>
                      </div>
                      <div className="mt-1 font-mono text-xs text-zinc-400">{artifact.file_path}</div>
                      {artifact.description && <p className="mt-2 text-sm text-zinc-300">{artifact.description}</p>}
                    </div>
                    <div className="text-sm text-zinc-400">
                      <div className="mb-1 text-[11px] uppercase tracking-[0.18em] text-zinc-500">Rules</div>
                      <div>{artifact.rules.length > 0 ? artifact.rules.join(', ') : 'None'}</div>
                    </div>
                    <div className="text-sm text-zinc-400">
                      <div className="mb-1 text-[11px] uppercase tracking-[0.18em] text-zinc-500">Permissions</div>
                      <div>{Object.keys(artifact.permissions).length > 0 ? Object.entries(artifact.permissions).map(([key, val]) => `${key}=${val}`).join(', ') : 'None'}</div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </section>

          <ScopeTaskPanel tasks={tasks} />
        </div>
      </div>
    </div>
  )
}
