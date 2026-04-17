import { useEffect, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Epic, Project } from '@/lib/types'

interface EpicCreateDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  projects: Project[]
  defaultProjectId?: string | null
  onCreated?: (epic: Epic) => void
}

const NONE_VALUE = '__none__'

export function EpicCreateDialog({
  open,
  onOpenChange,
  projects,
  defaultProjectId,
  onCreated,
}: EpicCreateDialogProps) {
  const api = useApi()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [projectId, setProjectId] = useState<string>(defaultProjectId ?? NONE_VALUE)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open) {
      setName('')
      setDescription('')
      setProjectId(defaultProjectId ?? NONE_VALUE)
    }
  }, [open, defaultProjectId])

  const trimmedName = name.trim()
  const canSubmit = trimmedName.length > 0

  async function handleSubmit() {
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const epic = await api.createEpic({
        name: trimmedName,
        description: description.trim(),
        project_id: projectId === NONE_VALUE ? null : projectId,
      })
      notifySuccess(`Created epic ${epic.name}`)
      onOpenChange(false)
      onCreated?.(epic)
    } catch (err) {
      notifyError(err, 'Failed to create epic')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New epic</DialogTitle>
          <DialogDescription>
            Epics group related tasks under a larger initiative.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="epic-name">Name *</Label>
            <Input
              id="epic-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Epic name"
              autoFocus
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="epic-description">Description</Label>
            <Textarea
              id="epic-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Optional description"
              rows={3}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Project</Label>
            <Select value={projectId} onValueChange={(v) => v && setProjectId(v)}>
              <SelectTrigger aria-label="Epic project" size="sm" className="h-8 w-full">
                <SelectValue placeholder="No project" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE_VALUE}>No project</SelectItem>
                {projects.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit || submitting}>
            {submitting ? 'Creating…' : 'Create epic'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
