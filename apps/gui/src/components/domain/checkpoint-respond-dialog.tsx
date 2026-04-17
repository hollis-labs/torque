import { useEffect, useState } from 'react'
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
import type { Checkpoint } from '@/lib/types'

interface CheckpointRespondDialogProps {
  checkpoint: Checkpoint | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onResponded?: () => void
}

function isValidJson(text: string): boolean {
  if (!text.trim()) return false
  try {
    JSON.parse(text)
    return true
  } catch {
    return false
  }
}

export function CheckpointRespondDialog({
  checkpoint,
  open,
  onOpenChange,
  onResponded,
}: CheckpointRespondDialogProps) {
  const api = useApi()
  const [responseJson, setResponseJson] = useState('')
  const [responderType, setResponderType] = useState('user')
  const [responderRef, setResponderRef] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open) {
      setResponseJson('')
      setResponderType('user')
      setResponderRef('')
    }
  }, [open])

  if (!checkpoint) return null

  const jsonValid = isValidJson(responseJson)
  const typeValid = responderType.trim().length > 0
  const canSubmit = jsonValid && typeValid

  async function handleSubmit() {
    if (!checkpoint || !canSubmit) return
    setSubmitting(true)
    try {
      await api.respondCheckpoint(checkpoint.correlation_id, {
        response_json: responseJson,
        responder_source_type: responderType.trim(),
        responder_source_ref: responderRef.trim() || undefined,
      })
      notifySuccess(`Responded to ${checkpoint.correlation_id}`)
      onOpenChange(false)
      onResponded?.()
    } catch (err) {
      notifyError(err, 'Failed to respond to checkpoint')
    } finally {
      setSubmitting(false)
    }
  }

  let formattedPayload = checkpoint.payload_json
  try {
    formattedPayload = JSON.stringify(JSON.parse(checkpoint.payload_json), null, 2)
  } catch {
    // keep raw string
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Respond to checkpoint</DialogTitle>
          <DialogDescription>
            Task {checkpoint.task_id} · {checkpoint.type}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label>Payload</Label>
            <pre className="whitespace-pre-wrap rounded-md border border-zinc-800 bg-zinc-950 p-3 font-mono text-xs text-zinc-300 max-h-48 overflow-auto">
              {formattedPayload}
            </pre>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cp-response">Response (JSON) *</Label>
            <Textarea
              id="cp-response"
              value={responseJson}
              onChange={(e) => setResponseJson(e.target.value)}
              placeholder='{"answer":"..."}'
              rows={5}
              className="font-mono text-xs"
              aria-invalid={!jsonValid && responseJson.length > 0}
            />
            {!jsonValid && responseJson.length > 0 && (
              <p className="text-xs text-red-400">Must be valid JSON</p>
            )}
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cp-source-type">Responder source type *</Label>
              <Input
                id="cp-source-type"
                value={responderType}
                onChange={(e) => setResponderType(e.target.value)}
                placeholder="user"
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cp-source-ref">Responder ref</Label>
              <Input
                id="cp-source-ref"
                value={responderRef}
                onChange={(e) => setResponderRef(e.target.value)}
                placeholder="optional"
              />
            </div>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit || submitting}>
            {submitting ? 'Submitting…' : 'Respond'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
