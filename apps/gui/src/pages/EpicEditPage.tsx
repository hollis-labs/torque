import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Save } from 'lucide-react'
import { Skeleton, Button, Input, Textarea, EmptyState } from '@hollis-labs/sysop-ui'
import { DetailHeader } from '@hollis-labs/sysop-ui/layout'
import { ScopeFormField } from '@/components/domain/scope-form-field'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { ContainerStatus, Epic, Project } from '@/lib/types'

export default function EpicEditPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const [epic, setEpic] = useState<Epic | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState({
    name: '',
    description: '',
    priority: '',
    project_id: '',
    status: 'active' as ContainerStatus,
  })

  useEffect(() => {
    if (!id) return
    let cancelled = false
    setLoading(true)
    void Promise.all([api.getEpic(id), api.listProjects()])
      .then(([nextEpic, projectRes]) => {
        if (cancelled) return
        setEpic(nextEpic)
        setProjects(projectRes.projects)
        setDraft({
          name: nextEpic.name,
          description: nextEpic.description ?? '',
          priority: nextEpic.priority === null || nextEpic.priority === undefined ? '' : String(nextEpic.priority),
          project_id: nextEpic.project_id ?? '',
          status: nextEpic.status,
        })
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

  async function save() {
    if (!id) return
    setSaving(true)
    try {
      await api.updateEpic(id, {
        name: draft.name.trim(),
        description: draft.description.trim(),
        priority: draft.priority.trim() ? Number(draft.priority.trim()) : null,
        project_id: draft.project_id || null,
        status: draft.status,
      })
      notifySuccess(`Updated ${draft.name.trim()}`)
      navigate(`/epics/${id}`)
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
        <Skeleton className="h-64 w-full rounded-lg" />
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
      <DetailHeader title={`Edit ${epic.name}`} backHref={`/epics/${epic.id}`} backLabel="Epic" id={epic.id} status={epic.status} />
      <div className="flex-1 overflow-auto p-6">
        <div className="grid gap-4 md:grid-cols-2">
          <ScopeFormField label="Name">
            <Input value={draft.name} onChange={(e) => setDraft((prev) => ({ ...prev, name: e.target.value }))} />
          </ScopeFormField>
          <ScopeFormField label="Status">
            <select className="h-9 rounded-md border border-zinc-800 bg-zinc-950 px-3 text-sm text-zinc-100" value={draft.status} onChange={(e) => setDraft((prev) => ({ ...prev, status: e.target.value as ContainerStatus }))}>
              <option value="active">Active</option>
              <option value="inactive">Inactive</option>
            </select>
          </ScopeFormField>
          <ScopeFormField label="Description" className="md:col-span-2">
            <Textarea rows={5} value={draft.description} onChange={(e) => setDraft((prev) => ({ ...prev, description: e.target.value }))} />
          </ScopeFormField>
          <ScopeFormField label="Project">
            <select className="h-9 rounded-md border border-zinc-800 bg-zinc-950 px-3 text-sm text-zinc-100" value={draft.project_id} onChange={(e) => setDraft((prev) => ({ ...prev, project_id: e.target.value }))}>
              <option value="">None</option>
              {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
            </select>
          </ScopeFormField>
          <ScopeFormField label="Priority">
            <Input value={draft.priority} onChange={(e) => setDraft((prev) => ({ ...prev, priority: e.target.value }))} placeholder="Optional integer" />
          </ScopeFormField>
          <div className="md:col-span-2 flex items-center gap-2">
            <Button onClick={save} disabled={saving || !draft.name.trim()}>
              <Save className="h-3.5 w-3.5" />
              {saving ? 'Saving…' : 'Save'}
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}
