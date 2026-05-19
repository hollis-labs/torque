import { useEffect, useState, type FormEvent } from 'react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@hollis-labs/sysop-ui'
import { Button, Input, Label, Textarea } from '@hollis-labs/sysop-ui'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { buildHITLCheckpointPayload } from '@/lib/hitl-request'
import { HITL_WORKFLOW_PRESETS, lookupHITLWorkflowDefinition } from '@/lib/hitl-workflows'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Artifact, Checkpoint, HITLWorkflowPreset, Task } from '@/lib/types'

const EMITTER_SOURCE_TYPES = ['user', 'agent', 'api', 'system', 'webhook', 'import'] as const

interface TaskHITLRequestDialogProps {
  task: Task
  artifacts: Artifact[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onRequested?: (checkpoint: Checkpoint) => void
}

function formatPayload(preset: HITLWorkflowPreset, task: Task, artifacts: Artifact[]): string {
  return JSON.stringify(buildHITLCheckpointPayload(preset, task, artifacts), null, 2)
}

function isValidJsonObject(value: string): boolean {
  try {
    const parsed = JSON.parse(value.trim())
    return parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)
  } catch {
    return false
  }
}

export function TaskHITLRequestDialog({
  task,
  artifacts,
  open,
  onOpenChange,
  onRequested,
}: TaskHITLRequestDialogProps) {
  const api = useApi()
  const [preset, setPreset] = useState<HITLWorkflowPreset>('pr_review')
  const [payloadJson, setPayloadJson] = useState('')
  const [emitterSourceType, setEmitterSourceType] = useState('user')
  const [emitterSourceRef, setEmitterSourceRef] = useState('gui')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (!open) return
    setPreset('pr_review')
    setPayloadJson(formatPayload('pr_review', task, artifacts))
    setEmitterSourceType('user')
    setEmitterSourceRef('gui')
  }, [open, task, artifacts])

  const workflow = lookupHITLWorkflowDefinition(preset)
  const payloadValid = isValidJsonObject(payloadJson)
  const canSubmit = payloadValid && emitterSourceType.trim() !== ''

  function handlePresetChange(value: string | null) {
    if (!value) return
    if (!HITL_WORKFLOW_PRESETS.includes(value as HITLWorkflowPreset)) return
    const next = value as HITLWorkflowPreset
    setPreset(next)
    setPayloadJson(formatPayload(next, task, artifacts))
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit) return

    setSubmitting(true)
    try {
      const checkpoint = await api.emitCheckpoint({
        task_id: task.id,
        type: preset,
        payload_json: payloadJson.trim(),
        emitter_source_type: emitterSourceType.trim(),
        emitter_source_ref: emitterSourceRef.trim() || undefined,
      })
      notifySuccess(`Requested ${workflow.title}`)
      onRequested?.(checkpoint)
      onOpenChange(false)
    } catch (err) {
      notifyError(err, 'Failed to request checkpoint')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !submitting && onOpenChange(next)}>
      <DialogContent className="sm:max-w-xl">
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Request HITL checkpoint</DialogTitle>
            <DialogDescription>
              Emit a typed checkpoint for task {task.id}. The payload is prefilled from task context and can be edited before sending.
            </DialogDescription>
          </DialogHeader>

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_180px]">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="hitl-request-type">Type</Label>
              <Select value={preset} onValueChange={handlePresetChange}>
                <SelectTrigger id="hitl-request-type" className="h-8 w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {HITL_WORKFLOW_PRESETS.map((type) => {
                    const definition = lookupHITLWorkflowDefinition(type)
                    return (
                      <SelectItem key={type} value={type}>
                        {definition.title}
                      </SelectItem>
                    )
                  })}
                </SelectContent>
              </Select>
              <p className="text-[11px] text-zinc-500">{workflow.description}</p>
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="hitl-emitter-source-type">Emitter</Label>
              <Select
                value={emitterSourceType}
                onValueChange={(value) => value && setEmitterSourceType(value)}
              >
                <SelectTrigger id="hitl-emitter-source-type" className="h-8 w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EMITTER_SOURCE_TYPES.map((type) => (
                    <SelectItem key={type} value={type}>
                      {type}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="hitl-emitter-source-ref">Emitter ref</Label>
            <Input
              id="hitl-emitter-source-ref"
              value={emitterSourceRef}
              onChange={(event) => setEmitterSourceRef(event.target.value)}
              placeholder="optional"
              disabled={submitting}
              autoComplete="off"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="hitl-payload-json">Payload JSON</Label>
            <Textarea
              id="hitl-payload-json"
              value={payloadJson}
              onChange={(event) => setPayloadJson(event.target.value)}
              rows={14}
              disabled={submitting}
              className="font-mono text-xs"
              aria-invalid={!payloadValid}
            />
            {!payloadValid && (
              <p className="text-[12px] text-destructive">Payload must be valid JSON.</p>
            )}
          </div>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={submitting}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={!canSubmit || submitting}>
              {submitting ? 'Requesting...' : 'Request checkpoint'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
