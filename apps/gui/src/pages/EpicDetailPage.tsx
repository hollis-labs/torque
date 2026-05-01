import { useState, useEffect, type ReactNode } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { ExternalLink, Pencil, Save, X } from 'lucide-react'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { DetailHeader } from '@/components/domain/detail-header'
import { PriorityBadge } from '@/components/domain/priority-badge'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { ContainerStatus, Epic, Project } from '@/lib/types'

const SSE_EVENTS = ['epic.updated', 'epic.created', 'epic.deleted']

export default function EpicDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [epic, setEpic] = useState<Epic | null>(null)
  const [projectName, setProjectName] = useState<string | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [draft, setDraft] = useState<{
    name: string
    description: string
    priority: string
    project_id: string
    status: ContainerStatus
  }>({
    name: '',
    description: '',
    priority: '',
    project_id: '',
    status: 'active',
  })

  useEffect(() => {
    if (!id) return
    let cancelled = false
    setLoading(true)
    void api.getEpic(id)
      .then(async (next) => {
        if (cancelled) return
        setEpic(next)
        setError(null)
        if (next.project_id) {
          const project = await api.getProject(next.project_id).catch(() => null)
          if (!cancelled) setProjectName(project?.name ?? null)
        } else {
          setProjectName(null)
        }
        if (!cancelled) {
          setDraft({
            name: next.name,
            description: next.description ?? '',
            priority: next.priority === null || next.priority === undefined ? '' : String(next.priority),
            project_id: next.project_id ?? '',
            status: next.status,
          })
        }
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
    void api.getEpic(id).then(setEpic).catch(() => {})
  }, [api, id, lastEvent])

  useEffect(() => {
    void api.listProjects().then((res) => setProjects(res.projects)).catch(() => {})
  }, [api])

  async function saveDraft() {
    if (!id) return
    setSaving(true)
    try {
      const updated = await api.updateEpic(id, {
        name: draft.name.trim(),
        description: draft.description.trim(),
        priority: draft.priority.trim() ? Number(draft.priority.trim()) : null,
        project_id: draft.project_id || null,
        status: draft.status,
      })
      setEpic(updated)
      setEditing(false)
      notifySuccess(`Updated ${updated.name}`)
    } catch (err) {
      notifyError(err, 'Failed to update epic')
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="p-6 flex flex-col gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-32 w-full rounded-lg" />
      </div>
    )
  }

  if (error || !epic) {
    return (
      <div className="p-6">
        <EmptyState variant="error" description={error ?? 'Epic not found.'} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <DetailHeader title={epic.name} backTo="/operations" backLabel="Operations" id={epic.id} status={epic.status}>
        {epic.priority !== null && epic.priority !== undefined && <PriorityBadge priority={epic.priority} />}
        <Button variant="outline" size="sm" onClick={() => setEditing((prev) => !prev)}>
          {editing ? <X className="h-3.5 w-3.5" /> : <Pencil className="h-3.5 w-3.5" />}
          {editing ? 'Cancel' : 'Edit'}
        </Button>
        <Button variant="outline" size="sm" onClick={() => navigate(`/operations?epic_id=${epic.id}`)}>
          <ExternalLink className="h-3.5 w-3.5" />
          Open in Operations
        </Button>
      </DetailHeader>

      <div className="flex-1 overflow-auto p-6">
        {editing ? (
          <div className="grid gap-4 md:grid-cols-2">
            <EditField label="Name">
              <Input value={draft.name} onChange={(e) => setDraft((prev) => ({ ...prev, name: e.target.value }))} />
            </EditField>
            <EditField label="Status">
              <select className="h-9 rounded-md border border-zinc-800 bg-zinc-950 px-3 text-sm text-zinc-100" value={draft.status} onChange={(e) => setDraft((prev) => ({ ...prev, status: e.target.value as ContainerStatus }))}>
                <option value="active">Active</option>
                <option value="inactive">Inactive</option>
              </select>
            </EditField>
            <EditField label="Description" className="md:col-span-2">
              <Textarea value={draft.description} onChange={(e) => setDraft((prev) => ({ ...prev, description: e.target.value }))} rows={4} />
            </EditField>
            <EditField label="Project">
              <select className="h-9 rounded-md border border-zinc-800 bg-zinc-950 px-3 text-sm text-zinc-100" value={draft.project_id} onChange={(e) => setDraft((prev) => ({ ...prev, project_id: e.target.value }))}>
                <option value="">None</option>
                {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
              </select>
            </EditField>
            <EditField label="Priority">
              <Input value={draft.priority} onChange={(e) => setDraft((prev) => ({ ...prev, priority: e.target.value }))} placeholder="Optional integer" />
            </EditField>
            <div className="md:col-span-2 flex items-center gap-2">
              <Button onClick={saveDraft} disabled={saving || !draft.name.trim()}>
                <Save className="h-3.5 w-3.5" />
                {saving ? 'Saving…' : 'Save'}
              </Button>
            </div>
          </div>
        ) : (
          <div className="grid gap-4 md:grid-cols-2">
            <InfoCard label="Description" value={epic.description || 'No description'} />
            <InfoCard label="Project" value={projectName || 'None'} />
          </div>
        )}
      </div>
    </div>
  )
}

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-4">
      <div className="mb-1 text-[11px] uppercase tracking-[0.18em] text-zinc-500">{label}</div>
      <div className="whitespace-pre-wrap text-sm text-zinc-200">{value}</div>
    </div>
  )
}

function EditField({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
  return (
    <div className={className}>
      <div className="mb-1 text-[11px] uppercase tracking-[0.18em] text-zinc-500">{label}</div>
      {children}
    </div>
  )
}
