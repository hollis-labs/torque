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

interface CheckpointCancelDialogProps {
  checkpoint: Checkpoint | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onCanceled?: () => void
}

export function CheckpointCancelDialog({
  checkpoint,
  open,
  onOpenChange,
  onCanceled,
}: CheckpointCancelDialogProps) {
  const api = useApi()
  const [reason, setReason] = useState('')
  const [cancelerType, setCancelerType] = useState('user')
  const [cancelerRef, setCancelerRef] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open) {
      setReason('')
      setCancelerType('user')
      setCancelerRef('')
    }
  }, [open])

  if (!checkpoint) return null

  async function handleSubmit() {
    if (!checkpoint) return
    setSubmitting(true)
    try {
      await api.cancelCheckpoint(checkpoint.correlation_id, {
        reason: reason.trim() || undefined,
        canceler_source_type: cancelerType.trim() || undefined,
        canceler_source_ref: cancelerRef.trim() || undefined,
      })
      notifySuccess(`Canceled ${checkpoint.correlation_id}`)
      onOpenChange(false)
      onCanceled?.()
    } catch (err) {
      notifyError(err, 'Failed to cancel checkpoint')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Cancel checkpoint</DialogTitle>
          <DialogDescription>
            Task {checkpoint.task_id} · {checkpoint.type}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cp-cancel-reason">Reason</Label>
            <Textarea
              id="cp-cancel-reason"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Optional human-readable reason"
              rows={3}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cp-cancel-type">Canceler source type</Label>
              <Input
                id="cp-cancel-type"
                value={cancelerType}
                onChange={(e) => setCancelerType(e.target.value)}
                placeholder="user"
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cp-cancel-ref">Canceler ref</Label>
              <Input
                id="cp-cancel-ref"
                value={cancelerRef}
                onChange={(e) => setCancelerRef(e.target.value)}
                placeholder="optional"
              />
            </div>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Keep
          </Button>
          <Button variant="destructive" onClick={handleSubmit} disabled={submitting}>
            {submitting ? 'Canceling…' : 'Cancel checkpoint'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
