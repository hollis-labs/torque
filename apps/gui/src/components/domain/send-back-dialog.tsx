import { useState, type FormEvent } from 'react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@hollis-labs/sysop-ui'
import { Button, Textarea, Label } from '@hollis-labs/sysop-ui'

const MAX_FEEDBACK_LENGTH = 2000

interface SendBackDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (feedback: string) => Promise<void>
}

export function SendBackDialog({ open, onOpenChange, onSubmit }: SendBackDialogProps) {
  const [feedback, setFeedback] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [showValidation, setShowValidation] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const trimmed = feedback.trim()
  const tooLong = feedback.length > MAX_FEEDBACK_LENGTH
  const invalid = trimmed.length === 0 || tooLong

  function handleOpenChange(next: boolean) {
    if (submitting) return
    if (!next) {
      setFeedback('')
      setShowValidation(false)
      setError(null)
    }
    onOpenChange(next)
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (invalid) {
      setShowValidation(true)
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      await onSubmit(trimmed)
      setFeedback('')
      setShowValidation(false)
      onOpenChange(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to send back')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Send back to todo</DialogTitle>
            <DialogDescription>
              The task will return to the queue and be re-dispatched with your feedback attached as a comment.
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-2">
            <Label htmlFor="send-back-feedback" className="text-[11px] uppercase tracking-[.18em] text-zinc-400">
              What needs to change?
            </Label>
            <Textarea
              id="send-back-feedback"
              value={feedback}
              onChange={(e) => {
                setFeedback(e.target.value)
                if (showValidation) setShowValidation(false)
              }}
              rows={6}
              placeholder="The agent will read this as feedback on the next run."
              disabled={submitting}
              autoFocus
              className="resize-none text-sm"
              aria-invalid={showValidation && invalid}
              aria-describedby="send-back-feedback-help"
            />
            <div
              id="send-back-feedback-help"
              className="flex items-center justify-between text-[11px]"
            >
              {showValidation && trimmed.length === 0 ? (
                <span className="text-destructive">Feedback is required.</span>
              ) : tooLong ? (
                <span className="text-destructive">
                  Feedback is too long ({feedback.length}/{MAX_FEEDBACK_LENGTH}).
                </span>
              ) : (
                <span className="text-zinc-500">Required — explains what the agent should do differently.</span>
              )}
              <span className={tooLong ? 'text-destructive' : 'text-zinc-500'}>
                {feedback.length}/{MAX_FEEDBACK_LENGTH}
              </span>
            </div>
            {error && <p className="text-[12px] text-destructive">{error}</p>}
          </div>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => handleOpenChange(false)}
              disabled={submitting}
            >
              Cancel
            </Button>
            <Button type="submit" variant="destructive" disabled={submitting || tooLong}>
              {submitting ? 'Sending…' : 'Send back to todo'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
