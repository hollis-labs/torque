import { useState, useEffect, type ReactNode } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { ExternalLink, Pencil, Save, X } from 'lucide-react'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { DetailHeader } from '@/components/domain/detail-header'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { ContainerStatus, Project, Sprint } from '@/lib/types'

const SSE_EVENTS = ['sprint.updated', 'sprint.created', 'sprint.deleted']

export default function SprintDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [sprint, setSprint] = useState<Sprint | null>(null)
  const [projectName, setProjectName] = useState<string | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [draft, setDraft] = useState<{
    name: string
    goal: string
    approval_mode: string
    cost_budget: string
    project_id: string
    status: ContainerStatus | 'completed'
  }>({
    name: '',
    goal: '',
    approval_mode: 'approve_each',
    cost_budget: '',
    project_id: '',
    status: 'active',
  })

  useEffect(() => {
    if (!id) return
    let cancelled = false
    setLoading(true)
    void api.getSprint(id)
      .then(async (next) => {
        if (cancelled) return
        setSprint(next)
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
            goal: next.goal ?? '',
            approval_mode: next.approval_mode || 'approve_each',
            cost_budget: next.cost_budget === null || next.cost_budget === undefined ? '' : String(next.cost_budget),
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
    void api.getSprint(id).then(setSprint).catch(() => {})
  }, [api, id, lastEvent])

  useEffect(() => {
    void api.listProjects().then((res) => setProjects(res.projects)).catch(() => {})
  }, [api])

  async function saveDraft() {
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
      const refreshed = await api.getSprint(id)
      setSprint(refreshed)
      setEditing(false)
      notifySuccess(`Updated ${refreshed.name}`)
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
        <Skeleton className="h-32 w-full rounded-lg" />
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
      <DetailHeader title={sprint.name} backTo="/operations" backLabel="Operations" id={sprint.id} status={sprint.status}>
        <Button variant="outline" size="sm" onClick={() => setEditing((prev) => !prev)}>
          {editing ? <X className="h-3.5 w-3.5" /> : <Pencil className="h-3.5 w-3.5" />}
          {editing ? 'Cancel' : 'Edit'}
        </Button>
        <Button variant="outline" size="sm" onClick={() => navigate(`/operations?sprint_id=${sprint.id}`)}>
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
              <select className="h-9 rounded-md border border-zinc-800 bg-zinc-950 px-3 text-sm text-zinc-100" value={draft.status} onChange={(e) => setDraft((prev) => ({ ...prev, status: e.target.value as ContainerStatus | 'completed' }))}>
                <option value="active">Active</option>
                <option value="inactive">Inactive</option>
                <option value="completed">Completed</option>
              </select>
            </EditField>
            <EditField label="Goal / Description" className="md:col-span-2">
              <Textarea value={draft.goal} onChange={(e) => setDraft((prev) => ({ ...prev, goal: e.target.value }))} rows={4} />
            </EditField>
            <EditField label="Project">
              <select className="h-9 rounded-md border border-zinc-800 bg-zinc-950 px-3 text-sm text-zinc-100" value={draft.project_id} onChange={(e) => setDraft((prev) => ({ ...prev, project_id: e.target.value }))}>
                <option value="">None</option>
                {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
              </select>
            </EditField>
            <EditField label="Approval Mode">
              <select className="h-9 rounded-md border border-zinc-800 bg-zinc-950 px-3 text-sm text-zinc-100" value={draft.approval_mode} onChange={(e) => setDraft((prev) => ({ ...prev, approval_mode: e.target.value }))}>
                <option value="approve_each">Approve each</option>
                <option value="approve_sprint">Approve sprint</option>
                <option value="auto">Auto</option>
              </select>
            </EditField>
            <EditField label="Cost Budget">
              <Input value={draft.cost_budget} onChange={(e) => setDraft((prev) => ({ ...prev, cost_budget: e.target.value }))} placeholder="Optional USD amount" />
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
            <InfoCard label="Goal / Description" value={sprint.goal || 'Not set'} />
            <InfoCard label="Project" value={projectName || 'None'} />
            <InfoCard label="Approval Mode" value={sprint.approval_mode || 'approve_each'} />
            <InfoCard label="Cost Budget" value={sprint.cost_budget === null || sprint.cost_budget === undefined ? 'None' : `$${sprint.cost_budget.toFixed(2)}`} />
            <InfoCard label="Started" value={sprint.started_at || 'Not set'} />
            <InfoCard label="Ended" value={sprint.ended_at || 'Not set'} />
          </div>
        )}
      </div>
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

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-4">
      <div className="mb-1 text-[11px] uppercase tracking-[0.18em] text-zinc-500">{label}</div>
      <div className="whitespace-pre-wrap text-sm text-zinc-200">{value}</div>
    </div>
  )
}
