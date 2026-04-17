import { useState, useEffect, useCallback } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DetailHeader } from '@/components/domain/detail-header'
import { EmptyState } from '@/components/domain/empty-state'
import { InstantiateTemplateDialog } from '@/components/domain/instantiate-template-dialog'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Template } from '@/lib/types'

interface FieldProps {
  label: string
  children: React.ReactNode
}

function Field({ label, children }: FieldProps) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-[10px] uppercase tracking-wider text-zinc-500">{label}</span>
      <div className="text-sm text-zinc-200">{children}</div>
    </div>
  )
}

function ChipList({ values }: { values: string[] }) {
  if (values.length === 0) return <span className="text-zinc-600">&mdash;</span>
  return (
    <div className="flex flex-wrap gap-1">
      {values.map((v) => (
        <span
          key={v}
          className="inline-block rounded border border-zinc-800 bg-zinc-900 px-2 py-0.5 font-mono text-xs text-zinc-300"
        >
          {v}
        </span>
      ))}
    </div>
  )
}

export default function TemplateDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [searchParams, setSearchParams] = useSearchParams()
  const api = useApi()

  const [versions, setVersions] = useState<number[]>([])
  const [template, setTemplate] = useState<Template | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showInstantiate, setShowInstantiate] = useState(false)

  const versionParam = searchParams.get('version')
  const action = searchParams.get('action')

  const fetchVersions = useCallback(async () => {
    if (!id) return
    try {
      const res = await api.listTemplates({ include_archived: true })
      const vs = res.templates.filter((t) => t.id === id).map((t) => t.version)
      vs.sort((a, b) => b - a)
      setVersions(vs)
    } catch {
      setVersions([])
    }
  }, [api, id])

  const fetchTemplate = useCallback(async () => {
    if (!id) return
    setLoading(true)
    try {
      const v = versionParam ? Number(versionParam) : undefined
      const t = await api.getTemplate(id, v)
      setTemplate(t)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load template')
    } finally {
      setLoading(false)
    }
  }, [api, id, versionParam])

  useEffect(() => {
    fetchVersions()
  }, [fetchVersions])

  useEffect(() => {
    fetchTemplate()
  }, [fetchTemplate])

  useEffect(() => {
    if (action === 'instantiate' && template) {
      setShowInstantiate(true)
      const next = new URLSearchParams(searchParams)
      next.delete('action')
      setSearchParams(next, { replace: true })
    }
  }, [action, template, searchParams, setSearchParams])

  async function handleArchive() {
    if (!template) return
    if (!confirm(`Archive ${template.id} v${template.version}?`)) return
    try {
      await api.archiveTemplate(template.id, template.version)
      notifySuccess(`Archived ${template.id} v${template.version}`)
      fetchTemplate()
      fetchVersions()
    } catch (err) {
      notifyError(err, 'Failed to archive template')
    }
  }

  async function handleDelete() {
    if (!template) return
    if (!confirm(`Delete all versions of ${template.id}?`)) return
    try {
      await api.deleteTemplate(template.id)
      notifySuccess(`Deleted ${template.id}`)
      window.location.href = '/templates'
    } catch (err) {
      notifyError(err, 'Failed to delete template')
    }
  }

  function handleVersionChange(value: string | null) {
    if (!value) return
    const next = new URLSearchParams(searchParams)
    next.set('version', value)
    setSearchParams(next, { replace: true })
  }

  if (loading && !template) {
    return (
      <div className="p-6 flex flex-col gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-5 w-96" />
        <Skeleton className="h-40 w-full rounded-lg" />
      </div>
    )
  }

  if (error || !template) {
    return (
      <div className="p-6">
        <EmptyState variant="error" description={error ?? 'Template not found.'} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <DetailHeader
        title={template.name}
        backTo="/templates"
        backLabel="Templates"
        id={template.id}
        status={template.is_archived ? 'archived' : 'active'}
      >
        <span className="rounded border border-zinc-800 bg-zinc-900 px-2 py-0.5 text-[10px] uppercase tracking-wider text-zinc-400">
          {template.kind}
        </span>
        {versions.length > 1 ? (
          <Select value={String(template.version)} onValueChange={handleVersionChange}>
            <SelectTrigger size="sm" className="h-7">
              <SelectValue>v{template.version}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              {versions.map((v) => (
                <SelectItem key={v} value={String(v)}>
                  v{v}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <span className="font-mono text-xs text-zinc-400">v{template.version}</span>
        )}
      </DetailHeader>

      <div className="flex items-center justify-between gap-2 border-b border-zinc-800/80 bg-zinc-950 px-6 py-2.5">
        <div className="text-xs text-zinc-500">
          {template.description || 'No description'}
        </div>
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            onClick={handleArchive}
            disabled={template.is_archived}
          >
            Archive
          </Button>
          <Button size="sm" variant="outline" onClick={handleDelete}>
            Delete
          </Button>
          <Button size="sm" onClick={() => setShowInstantiate(true)}>
            Instantiate
          </Button>
        </div>
      </div>

      <div className="flex-1 overflow-auto p-6">
        <div className="grid grid-cols-2 gap-6 max-w-4xl">
          <Field label="Auto-execute">{template.auto_execute ? 'yes' : 'no'}</Field>
          <Field label="On done">{template.on_done}</Field>
          <Field label="On fail">{template.on_fail}</Field>
          <Field label="On review">{template.on_review}</Field>
          <Field label="On done merge">{template.on_done_merge}</Field>
          <Field label="Checkpoint mode">{template.checkpoint_mode}</Field>
          <Field label="Executor">
            {template.executor ? (
              <span className="font-mono text-xs">{template.executor}</span>
            ) : (
              <span className="text-zinc-600">&mdash;</span>
            )}
          </Field>
          <Field label="Agent profile">
            {template.agent_profile ? (
              <span className="font-mono text-xs">{template.agent_profile}</span>
            ) : (
              <span className="text-zinc-600">&mdash;</span>
            )}
          </Field>

          <div className="col-span-2">
            <Field label="Required vars">
              <ChipList values={template.required_vars} />
            </Field>
          </div>

          <div className="col-span-2">
            <Field label="Tags">
              <ChipList values={template.tags} />
            </Field>
          </div>

          <div className="col-span-2">
            <Field label="Tools">
              <ChipList values={template.tools} />
            </Field>
          </div>

          <div className="col-span-2">
            <Field label="Deliverables">
              {template.deliverables.length === 0 ? (
                <span className="text-zinc-600">&mdash;</span>
              ) : (
                <ul className="flex flex-col gap-1 text-xs">
                  {template.deliverables.map((d, i) => (
                    <li key={i} className="font-mono text-zinc-300">
                      {d.type}
                      {d.required ? ' (required)' : ''}
                      {d.description ? ` — ${d.description}` : ''}
                    </li>
                  ))}
                </ul>
              )}
            </Field>
          </div>

          {template.system_prompt && (
            <div className="col-span-2">
              <Field label="System prompt">
                <pre className="whitespace-pre-wrap rounded-md border border-zinc-800 bg-zinc-950 p-3 font-mono text-xs text-zinc-300">
                  {template.system_prompt}
                </pre>
              </Field>
            </div>
          )}

          {Object.keys(template.metadata_template).length > 0 && (
            <div className="col-span-2">
              <Field label="Metadata template">
                <pre className="whitespace-pre-wrap rounded-md border border-zinc-800 bg-zinc-950 p-3 font-mono text-xs text-zinc-300">
                  {JSON.stringify(template.metadata_template, null, 2)}
                </pre>
              </Field>
            </div>
          )}
        </div>
      </div>

      <InstantiateTemplateDialog
        template={template}
        open={showInstantiate}
        onOpenChange={setShowInstantiate}
      />
    </div>
  )
}
