import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Save } from 'lucide-react'
import { Skeleton, Button, Input, Textarea, EmptyState } from '@hollis-labs/sysop-ui'
import { DetailHeader } from '@hollis-labs/sysop-ui/layout'
import { ScopeFormField } from '@/components/domain/scope-form-field'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { ContainerStatus, Project, Sprint } from '@/lib/types'

export default function SprintEditPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const [sprint, setSprint] = useState<Sprint | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState({
    name: '',
    goal: '',
    approval_mode: 'approve_each',
    cost_budget: '',
    project_id: '',
    status: 'active' as ContainerStatus,
  })

  useEffect(() => {
    if (!id) return
    let cancelled = false
    setLoading(true)
    void Promise.all([api.getSprint(id), api.listProjects()])
      .then(([nextSprint, projectRes]) => {
        if (cancelled) return
        setSprint(nextSprint)
        setProjects(projectRes.projects)
        setDraft({
          name: nextSprint.name,
          goal: nextSprint.goal ?? '',
          approval_mode: nextSprint.approval_mode || 'approve_each',
          cost_budget: nextSprint.cost_budget === null || nextSprint.cost_budget === undefined ? '' : String(nextSprint.cost_budget),
          project_id: nextSprint.project_id ?? '',
          status: nextSprint.status,
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
    if (!id || !sprint) return
    setSaving(true)
    try {
      await api.updateSprint(id, {
        name: draft.name.trim(),
        goal: draft.goal.trim(),
        approval_mode: draft.approval_mode,
        cost_budget: draft.cost_budget.trim() ? Number(draft.cost_budget.trim()) : null,
        project_id: draft.project_id || null,
      })
      if (draft.status !== sprint.status) {
        await api.transitionSprint(id, draft.status)
      }
      notifySuccess(`Updated ${draft.name.trim()}`)
      navigate(`/sprints/${id}`)
    } catch (err) {
      notifyError(err, 'Failed to update sprint')
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

  if (error || !sprint) {
    return (
      <div className="p-6">
        <EmptyState variant="error" title="Something went wrong" description={error ?? 'Sprint not found.'} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <DetailHeader title={`Edit ${sprint.name}`} backHref={`/sprints/${sprint.id}`} backLabel="Sprint" id={sprint.id} status={sprint.status} />
      <div className="flex-1 overflow-auto p-6">
        <div className="grid gap-4 md:grid-cols-2">
          <ScopeFormField label="Name">
            <Input value={draft.name} onChange={(e) => setDraft((prev) => ({ ...prev, name: e.target.value }))} />
          </ScopeFormField>
          <ScopeFormField label="Status">
            <select className="h-9 rounded-md border border-zinc-800 bg-zinc-950 px-3 text-sm text-zinc-100" value={draft.status} onChange={(e) => setDraft((prev) => ({ ...prev, status: e.target.value as ContainerStatus }))}>
              <option value="active">Active</option>
              <option value="inactive">Inactive</option>
              <option value="completed">Completed</option>
            </select>
          </ScopeFormField>
          <ScopeFormField label="Goal / Description" className="md:col-span-2">
            <Textarea rows={5} value={draft.goal} onChange={(e) => setDraft((prev) => ({ ...prev, goal: e.target.value }))} />
          </ScopeFormField>
          <ScopeFormField label="Project">
            <select className="h-9 rounded-md border border-zinc-800 bg-zinc-950 px-3 text-sm text-zinc-100" value={draft.project_id} onChange={(e) => setDraft((prev) => ({ ...prev, project_id: e.target.value }))}>
              <option value="">None</option>
              {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
            </select>
          </ScopeFormField>
          <ScopeFormField label="Approval Mode">
            <select className="h-9 rounded-md border border-zinc-800 bg-zinc-950 px-3 text-sm text-zinc-100" value={draft.approval_mode} onChange={(e) => setDraft((prev) => ({ ...prev, approval_mode: e.target.value }))}>
              <option value="approve_each">Approve each</option>
              <option value="approve_sprint">Approve sprint</option>
              <option value="auto">Auto</option>
            </select>
          </ScopeFormField>
          <ScopeFormField label="Cost Budget">
            <Input value={draft.cost_budget} onChange={(e) => setDraft((prev) => ({ ...prev, cost_budget: e.target.value }))} placeholder="Optional USD amount" />
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
