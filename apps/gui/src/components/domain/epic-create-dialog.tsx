import { useEffect, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@hollis-labs/sysop-ui'
import { ScopePicker } from './scope-picker'
import { Button, Input, Label, Textarea } from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Epic } from '@/lib/types'

interface EpicCreateDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  defaultProjectId?: string | null
  onCreated?: (epic: Epic) => void
}

const NONE_VALUE = '__none__'

export function EpicCreateDialog({
  open,
  onOpenChange,
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
            <ScopePicker kind="project" label="Epic project" value={projectId === NONE_VALUE ? null : projectId} onChange={id => setProjectId(id ?? NONE_VALUE)} />
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
