import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Save } from 'lucide-react'
import { Skeleton, Button, Input, Textarea, EmptyState } from '@hollis-labs/sysop-ui'
import { DetailHeader } from '@hollis-labs/sysop-ui/layout'
import { ScopeFormField } from '@/components/domain/scope-form-field'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { ContainerStatus, Project } from '@/lib/types'

function linesToArray(value: string): string[] {
  return value.split('\n').map((line) => line.trim()).filter(Boolean)
}

function parsePermissions(value: string): Record<string, string> {
  return value
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .reduce<Record<string, string>>((acc, line) => {
      const [key, ...rest] = line.split('=')
      if (!key) return acc
      acc[key.trim()] = rest.join('=').trim()
      return acc
    }, {})
}

export default function ProjectEditPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const [project, setProject] = useState<Project | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState({
    name: '',
    description: '',
    repo_path: '',
    agent_path: '',
    read_paths: '',
    write_paths: '',
    context_paths: '',
    permissions: '',
    rules: '',
    status: 'active' as ContainerStatus,
  })

  useEffect(() => {
    if (!id) return
    let cancelled = false
    setLoading(true)
    void api.getProject(id)
      .then((next) => {
        if (cancelled) return
        setProject(next)
        setDraft({
          name: next.name,
          description: next.description ?? '',
          repo_path: next.repo_path ?? '',
          agent_path: next.agent_path ?? '',
          read_paths: next.read_paths.join('\n'),
          write_paths: next.write_paths.join('\n'),
          context_paths: next.context_paths.join('\n'),
          permissions: Object.entries(next.permissions).map(([key, value]) => `${key}=${value}`).join('\n'),
          rules: next.rules.join('\n'),
          status: next.status,
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
      const updated = await api.updateProject(id, {
        name: draft.name.trim(),
        description: draft.description.trim(),
        repo_path: draft.repo_path.trim(),
        agent_path: draft.agent_path.trim(),
        read_paths: linesToArray(draft.read_paths),
        write_paths: linesToArray(draft.write_paths),
        context_paths: linesToArray(draft.context_paths),
        permissions: parsePermissions(draft.permissions),
        rules: linesToArray(draft.rules),
        status: draft.status,
      })
      notifySuccess(`Updated ${updated.name}`)
      navigate(`/projects/${updated.id}`)
    } catch (err) {
      notifyError(err, 'Failed to update project')
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

  if (error || !project) {
    return (
      <div className="p-6">
        <EmptyState variant="error" title="Something went wrong" description={error ?? 'Project not found.'} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <DetailHeader title={`Edit ${project.name}`} backHref={`/projects/${project.id}`} backLabel="Project" id={project.id} status={project.status} />
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
          <ScopeFormField label="Repo Path">
            <Input value={draft.repo_path} onChange={(e) => setDraft((prev) => ({ ...prev, repo_path: e.target.value }))} />
          </ScopeFormField>
          <ScopeFormField label="Agent Path">
            <Input value={draft.agent_path} onChange={(e) => setDraft((prev) => ({ ...prev, agent_path: e.target.value }))} />
          </ScopeFormField>
          <ScopeFormField label="Read Paths">
            <Textarea rows={5} value={draft.read_paths} onChange={(e) => setDraft((prev) => ({ ...prev, read_paths: e.target.value }))} />
          </ScopeFormField>
          <ScopeFormField label="Write Paths">
            <Textarea rows={5} value={draft.write_paths} onChange={(e) => setDraft((prev) => ({ ...prev, write_paths: e.target.value }))} />
          </ScopeFormField>
          <ScopeFormField label="Context Paths">
            <Textarea rows={5} value={draft.context_paths} onChange={(e) => setDraft((prev) => ({ ...prev, context_paths: e.target.value }))} />
          </ScopeFormField>
          <ScopeFormField label="Permissions">
            <Textarea rows={5} value={draft.permissions} onChange={(e) => setDraft((prev) => ({ ...prev, permissions: e.target.value }))} />
          </ScopeFormField>
          <ScopeFormField label="Rules" className="md:col-span-2">
            <Textarea rows={5} value={draft.rules} onChange={(e) => setDraft((prev) => ({ ...prev, rules: e.target.value }))} />
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
