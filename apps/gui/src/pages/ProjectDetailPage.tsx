import { useState, useEffect } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { ExternalLink, FileText, FolderOpen } from 'lucide-react'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { DetailHeader } from '@/components/domain/detail-header'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { isHtmlApiFallbackError } from '@/lib/api'
import type { Project, ProjectArtifact } from '@/lib/types'

const SSE_EVENTS = ['project.updated', 'project.created', 'project.deleted']

function MetaBlock({ label, value }: { label: string; value: string | string[] | Record<string, string> }) {
  let content: string
  if (Array.isArray(value)) content = value.join('\n')
  else if (typeof value === 'object') content = Object.entries(value).map(([key, val]) => `${key}=${val}`).join('\n')
  else content = value
  if (!content.trim()) return null
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-3">
      <div className="mb-1 text-[11px] uppercase tracking-[0.18em] text-zinc-500">{label}</div>
      <pre className="whitespace-pre-wrap break-words text-sm text-zinc-200">{content}</pre>
    </div>
  )
}

export default function ProjectDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [project, setProject] = useState<Project | null>(null)
  const [artifacts, setArtifacts] = useState<ProjectArtifact[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [artifactWarning, setArtifactWarning] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    let cancelled = false
    setLoading(true)
    void Promise.all([
      api.getProject(id),
      api.listProjectArtifacts(id).catch(() => ({ artifacts: [] as ProjectArtifact[] })),
    ])
      .then(([nextProject, artifactRes]) => {
        if (cancelled) return
        setProject(nextProject)
        setArtifacts(artifactRes.artifacts)
        setArtifactWarning(null)
        setError(null)
      })
      .catch((err: Error) => {
        if (!cancelled) setError(err.message)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [api, id])

  useEffect(() => {
    if (!lastEvent || !id) return
    void api.getProject(id).then(setProject).catch(() => {})
    void api.listProjectArtifacts(id)
      .then((res) => {
        setArtifacts(res.artifacts)
        setArtifactWarning(null)
      })
      .catch((err) => {
        if (isHtmlApiFallbackError(err) || (err instanceof Error && /HTTP 404|not found/i.test(err.message))) {
          setArtifacts([])
          setArtifactWarning('Project artifacts are unavailable from the current backend runtime. The API route is missing or stale.')
        }
      })
  }, [api, id, lastEvent])

  if (loading) {
    return (
      <div className="p-6 flex flex-col gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-32 w-full rounded-lg" />
        <Skeleton className="h-48 w-full rounded-lg" />
      </div>
    )
  }

  if (error || !project) {
    return (
      <div className="p-6">
        <EmptyState variant="error" description={error ?? 'Project not found.'} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <DetailHeader title={project.name} backTo="/operations" backLabel="Operations" id={project.id} status={project.status}>
        <Button variant="outline" size="sm" onClick={() => navigate(`/operations?project_id=${project.id}`)}>
          <ExternalLink className="h-3.5 w-3.5" />
          Open in Operations
        </Button>
      </DetailHeader>

      <div className="flex-1 overflow-auto p-6">
        <div className="grid gap-4 md:grid-cols-2">
          <MetaBlock label="Description" value={project.description} />
          <MetaBlock label="Project Path" value={project.repo_path} />
          <MetaBlock label="Agent Path" value={project.agent_path} />
          <MetaBlock label="Read Paths" value={project.read_paths} />
          <MetaBlock label="Write Paths" value={project.write_paths} />
          <MetaBlock label="Additional Context Dirs" value={project.context_paths} />
          <MetaBlock label="Permissions" value={project.permissions} />
          <MetaBlock label="Rules" value={project.rules} />
        </div>

        <div className="mt-8 rounded-xl border border-zinc-800 bg-zinc-950/60">
          <div className="border-b border-zinc-800 px-4 py-3">
            <h2 className="text-sm font-semibold text-zinc-100">Artifacts</h2>
            <p className="mt-1 text-sm text-zinc-500">Authoritative project documents, folders, and references inherited by tasks at run time.</p>
          </div>
          {artifactWarning && (
            <div className="border-b border-amber-700/40 bg-amber-950/30 px-4 py-3 text-sm text-amber-200">
              {artifactWarning}
            </div>
          )}
          {artifacts.length === 0 ? (
            <div className="px-4 py-6 text-sm text-zinc-500">No project artifacts registered yet.</div>
          ) : (
            <div className="divide-y divide-zinc-800">
              {artifacts.map((artifact) => (
                <div key={artifact.id} className="grid gap-2 px-4 py-3 md:grid-cols-[1fr,160px,220px]">
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
                    <div>
                      {Object.keys(artifact.permissions).length > 0
                        ? Object.entries(artifact.permissions).map(([key, val]) => `${key}=${val}`).join(', ')
                        : 'None'}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
