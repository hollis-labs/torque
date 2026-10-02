import { useEffect, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@hollis-labs/sysop-ui'
import { Button, Input, Label, Textarea } from '@hollis-labs/sysop-ui'
import { ScopePicker } from './scope-picker'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Sprint } from '@/lib/types'

interface SprintCreateDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  defaultProjectId?: string | null
  onCreated?: (sprint: Sprint) => void
}

const NONE_VALUE = '__none__'

export function SprintCreateDialog({
  open,
  onOpenChange,
  defaultProjectId,
  onCreated,
}: SprintCreateDialogProps) {
  const api = useApi()
  const [name, setName] = useState('')
  const [goal, setGoal] = useState('')
  const [projectId, setProjectId] = useState<string>(defaultProjectId ?? NONE_VALUE)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open) {
      setName('')
      setGoal('')
      setProjectId(defaultProjectId ?? NONE_VALUE)
    }
  }, [open, defaultProjectId])

  const trimmedName = name.trim()
  const canSubmit = trimmedName.length > 0

  async function handleSubmit() {
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const sprint = await api.createSprint({
        name: trimmedName,
        goal: goal.trim(),
        project_id: projectId === NONE_VALUE ? null : projectId,
      })
      notifySuccess(`Created sprint ${sprint.name}`)
      onOpenChange(false)
      onCreated?.(sprint)
    } catch (err) {
      notifyError(err, 'Failed to create sprint')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New sprint</DialogTitle>
          <DialogDescription>
            Create a sprint and jump straight into its filtered task scope.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="sprint-name">Name *</Label>
            <Input
              id="sprint-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Sprint name"
              autoFocus
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="sprint-goal">Goal</Label>
            <Textarea
              id="sprint-goal"
              value={goal}
              onChange={(e) => setGoal(e.target.value)}
              placeholder="Optional sprint goal"
              rows={3}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Project</Label>
            <ScopePicker kind="project" label="Sprint project" value={projectId === NONE_VALUE ? null : projectId} onChange={id => setProjectId(id ?? NONE_VALUE)} />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit || submitting}>
            {submitting ? 'Creating…' : 'Create sprint'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
