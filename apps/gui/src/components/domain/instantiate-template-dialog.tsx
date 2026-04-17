import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Template, TemplateInstantiateRequest } from '@/lib/types'

interface InstantiateTemplateDialogProps {
  template: Template | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

function isJsonObject(text: string): boolean {
  if (!text.trim()) return true
  try {
    const parsed = JSON.parse(text)
    return typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)
  } catch {
    return false
  }
}

export function InstantiateTemplateDialog({
  template,
  open,
  onOpenChange,
}: InstantiateTemplateDialogProps) {
  const api = useApi()
  const navigate = useNavigate()

  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [vars, setVars] = useState<Record<string, string>>({})
  const [overridesJson, setOverridesJson] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open && template) {
      setTitle('')
      setDescription('')
      const initial: Record<string, string> = {}
      for (const key of template.required_vars) initial[key] = ''
      setVars(initial)
      setOverridesJson('')
    }
  }, [open, template])

  if (!template) return null

  const missingVars = template.required_vars.filter((v) => !(vars[v] && vars[v].trim()))
  const overridesValid = isJsonObject(overridesJson)
  const canSubmit = title.trim().length > 0 && missingVars.length === 0 && overridesValid

  async function handleSubmit() {
    if (!template || !canSubmit) return
    setSubmitting(true)
    const body: TemplateInstantiateRequest = { title: title.trim() }
    if (description.trim()) body.description = description.trim()
    if (Object.keys(vars).length > 0) body.vars = vars
    if (overridesJson.trim()) {
      try {
        body.overrides = JSON.parse(overridesJson) as Record<string, unknown>
      } catch {
        notifyError(new Error('Invalid overrides JSON'), 'Invalid overrides JSON')
        setSubmitting(false)
        return
      }
    }
    try {
      const task = await api.instantiateTemplate(template.id, body)
      notifySuccess(`Created task ${task.id}`)
      onOpenChange(false)
      navigate(`/tasks/${task.id}`)
    } catch (err) {
      notifyError(err, 'Failed to instantiate template')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Instantiate {template.name}</DialogTitle>
          <DialogDescription>
            Create a new task from this template (v{template.version}).
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tpl-title">Title *</Label>
            <Input
              id="tpl-title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Task title"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tpl-description">Description</Label>
            <Textarea
              id="tpl-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Optional task description"
              rows={3}
            />
          </div>

          {template.required_vars.length > 0 && (
            <div className="flex flex-col gap-2">
              <Label>Required vars</Label>
              <div className="flex flex-col gap-2 rounded-md border border-zinc-800 bg-zinc-950 p-3">
                {template.required_vars.map((key) => (
                  <div key={key} className="flex items-center gap-2">
                    <span className="w-32 shrink-0 font-mono text-xs text-zinc-400">{key}</span>
                    <Input
                      value={vars[key] ?? ''}
                      onChange={(e) =>
                        setVars((prev) => ({ ...prev, [key]: e.target.value }))
                      }
                      placeholder={`value for {{${key}}}`}
                      className="flex-1"
                    />
                  </div>
                ))}
              </div>
            </div>
          )}

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tpl-overrides">
              Overrides (JSON object)
            </Label>
            <Textarea
              id="tpl-overrides"
              value={overridesJson}
              onChange={(e) => setOverridesJson(e.target.value)}
              placeholder='{"priority": 1, "tags": ["urgent"]}'
              rows={4}
              className="font-mono text-xs"
              aria-invalid={!overridesValid}
            />
            {!overridesValid && (
              <p className="text-xs text-red-400">Must be a valid JSON object</p>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit || submitting}>
            {submitting ? 'Creating…' : 'Create task'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
