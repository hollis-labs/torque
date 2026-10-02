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
import type { PlanDetail, PlanPhaseInput } from '@/lib/types'

interface CreatePlanDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  defaultProjectId?: string | null
  defaultSprintId?: string | null
  onCreated?: (plan: PlanDetail) => void
}

const NONE_VALUE = '__none__'

export function CreatePlanDialog({
  open,
  onOpenChange,
  defaultProjectId,
  defaultSprintId,
  onCreated,
}: CreatePlanDialogProps) {
  const api = useApi()
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [projectId, setProjectId] = useState<string>(defaultProjectId ?? NONE_VALUE)
  const [sprintId, setSprintId] = useState<string>(defaultSprintId ?? NONE_VALUE)
  const [phasesText, setPhasesText] = useState('Foundation, Integration, Rollout')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open) {
      setTitle('')
      setDescription('')
      setProjectId(defaultProjectId ?? NONE_VALUE)
      setSprintId(defaultSprintId ?? NONE_VALUE)
      setPhasesText('Foundation, Integration, Rollout')
    }
  }, [open, defaultProjectId, defaultSprintId])

  const trimmedTitle = title.trim()
  const canSubmit = trimmedTitle.length > 0 && !submitting

  function parsePhases(): PlanPhaseInput[] {
    return phasesText
      .split(',')
      .map((s) => s.trim())
      .filter((s) => s.length > 0)
      .map((name) => ({ name }))
  }

  async function handleSubmit() {
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const plan = await api.createPlan({
        title: trimmedTitle,
        description: description.trim(),
        project_id: projectId === NONE_VALUE ? undefined : projectId,
        sprint_id: sprintId === NONE_VALUE ? undefined : sprintId,
        phases: parsePhases(),
      })
      notifySuccess(`Created plan ${plan.task.title}`)
      onOpenChange(false)
      onCreated?.(plan)
    } catch (err) {
      notifyError(err, 'Failed to create plan')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New plan</DialogTitle>
          <DialogDescription>
            Plans coordinate phase-scoped child tasks. Each phase groups child
            tasks linked by parent_id + metadata.phase_id.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="plan-title">Title *</Label>
            <Input
              id="plan-title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="e.g. Plans v1"
              autoFocus
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="plan-description">Description</Label>
            <Textarea
              id="plan-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Optional summary of what this plan delivers"
              rows={3}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="plan-phases">Initial phases</Label>
            <Input
              id="plan-phases"
              value={phasesText}
              onChange={(e) => setPhasesText(e.target.value)}
              placeholder="comma-separated phase names"
            />
            <p className="text-[10px] text-zinc-500">
              Comma-separated. IDs and order are auto-assigned.
            </p>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Project</Label>
            <ScopePicker kind="project" label="Plan project" value={projectId === NONE_VALUE ? null : projectId} onChange={id => { setProjectId(id ?? NONE_VALUE); setSprintId(NONE_VALUE) }} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Sprint</Label>
            <ScopePicker kind="sprint" label="Plan sprint" projectId={projectId === NONE_VALUE ? undefined : projectId} value={sprintId === NONE_VALUE ? null : sprintId} onChange={id => setSprintId(id ?? NONE_VALUE)} />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit}>
            {submitting ? 'Creating…' : 'Create plan'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
