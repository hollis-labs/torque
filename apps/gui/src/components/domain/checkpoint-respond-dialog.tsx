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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { useApi } from '@/hooks/use-api'
import { lookupHITLWorkflowDefinition } from '@/lib/hitl-workflows'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Checkpoint } from '@/lib/types'

interface CheckpointRespondDialogProps {
  checkpoint: Checkpoint | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onResponded?: () => void
}

interface ResponseFormState {
  prDecision: 'approve' | 'request_changes' | 'comment'
  prSummary: string
  prComments: string
  prRequiredChanges: string
  approvalDecision: 'approved' | 'rejected' | 'needs_info'
  approvalComment: string
  messageAcknowledged: boolean
  messageReply: string
  genericResponse: string
}

function createInitialResponseForm(): ResponseFormState {
  return {
    prDecision: 'approve',
    prSummary: '',
    prComments: '',
    prRequiredChanges: '',
    approvalDecision: 'approved',
    approvalComment: '',
    messageAcknowledged: true,
    messageReply: '',
    genericResponse: '',
  }
}

function linesToArray(text: string): string[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
}

function serializeKnownResponse(type: string, form: ResponseFormState): string {
  if (type === 'pr_review') {
    const response: {
      decision: ResponseFormState['prDecision']
      summary?: string
      comments?: string[]
      required_changes?: string[]
    } = { decision: form.prDecision }
    const summary = form.prSummary.trim()
    const comments = linesToArray(form.prComments)
    const requiredChanges = linesToArray(form.prRequiredChanges)
    if (summary) response.summary = summary
    if (comments.length > 0) response.comments = comments
    if (requiredChanges.length > 0) response.required_changes = requiredChanges
    return JSON.stringify(response)
  }

  if (type === 'approval') {
    const response: {
      decision: ResponseFormState['approvalDecision']
      comment?: string
    } = { decision: form.approvalDecision }
    const comment = form.approvalComment.trim()
    if (comment) response.comment = comment
    return JSON.stringify(response)
  }

  if (type === 'message') {
    const response: {
      acknowledged: boolean
      reply?: string
    } = { acknowledged: form.messageAcknowledged }
    const reply = form.messageReply.trim()
    if (reply) response.reply = reply
    return JSON.stringify(response)
  }

  return JSON.stringify({ response: form.genericResponse.trim() })
}

export function CheckpointRespondDialog({
  checkpoint,
  open,
  onOpenChange,
  onResponded,
}: CheckpointRespondDialogProps) {
  const api = useApi()
  const [form, setForm] = useState<ResponseFormState>(createInitialResponseForm)
  const [responderType, setResponderType] = useState('user')
  const [responderRef, setResponderRef] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open) {
      setForm(createInitialResponseForm())
      setResponderType('user')
      setResponderRef('')
    }
  }, [open, checkpoint?.type])

  if (!checkpoint) return null

  const workflow = lookupHITLWorkflowDefinition(checkpoint.type)
  const knownWorkflow = workflow.known
  const responseType = knownWorkflow ? workflow.type : checkpoint.type
  const typeValid = responderType.trim().length > 0
  const canSubmit = typeValid && (knownWorkflow || form.genericResponse.trim().length > 0)

  async function handleSubmit() {
    if (!checkpoint || !canSubmit) return
    setSubmitting(true)
    try {
      await api.respondCheckpoint(checkpoint.correlation_id, {
        response_json: serializeKnownResponse(responseType, form),
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

  const configuredResponderTypes = workflow.task_metadata.requirements.allowed_responder_source_types
  const allowedResponderTypes =
    configuredResponderTypes && configuredResponderTypes.length > 0
      ? configuredResponderTypes
      : ['user', 'agent', 'api']

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
            Task {checkpoint.task_id} · {workflow.title}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label>Payload</Label>
            <pre className="whitespace-pre-wrap rounded-md border border-zinc-800 bg-zinc-950 p-3 font-mono text-xs text-zinc-300 max-h-48 overflow-auto">
              {formattedPayload}
            </pre>
          </div>

          {responseType === 'pr_review' && (
            <div className="flex flex-col gap-3">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="cp-pr-decision">Decision *</Label>
                <Select
                  value={form.prDecision}
                  onValueChange={(value) => {
                    if (value === 'approve' || value === 'request_changes' || value === 'comment') {
                      setForm((current) => ({ ...current, prDecision: value }))
                    }
                  }}
                >
                  <SelectTrigger id="cp-pr-decision" className="h-8 w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="approve">Approve</SelectItem>
                    <SelectItem value="request_changes">Request changes</SelectItem>
                    <SelectItem value="comment">Comment</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="cp-pr-summary">Summary</Label>
                <Textarea
                  id="cp-pr-summary"
                  value={form.prSummary}
                  onChange={(e) => setForm((current) => ({ ...current, prSummary: e.target.value }))}
                  rows={3}
                  placeholder="Review summary"
                />
              </div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="cp-pr-comments">Comments</Label>
                  <Textarea
                    id="cp-pr-comments"
                    value={form.prComments}
                    onChange={(e) => setForm((current) => ({ ...current, prComments: e.target.value }))}
                    rows={4}
                    placeholder="One comment per line"
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="cp-pr-required-changes">Required changes</Label>
                  <Textarea
                    id="cp-pr-required-changes"
                    value={form.prRequiredChanges}
                    onChange={(e) => setForm((current) => ({ ...current, prRequiredChanges: e.target.value }))}
                    rows={4}
                    placeholder="One required change per line"
                  />
                </div>
              </div>
            </div>
          )}

          {responseType === 'approval' && (
            <div className="flex flex-col gap-3">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="cp-approval-decision">Decision *</Label>
                <Select
                  value={form.approvalDecision}
                  onValueChange={(value) => {
                    if (value === 'approved' || value === 'rejected' || value === 'needs_info') {
                      setForm((current) => ({ ...current, approvalDecision: value }))
                    }
                  }}
                >
                  <SelectTrigger id="cp-approval-decision" className="h-8 w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="approved">Approved</SelectItem>
                    <SelectItem value="rejected">Rejected</SelectItem>
                    <SelectItem value="needs_info">Needs info</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="cp-approval-comment">Comment</Label>
                <Textarea
                  id="cp-approval-comment"
                  value={form.approvalComment}
                  onChange={(e) => setForm((current) => ({ ...current, approvalComment: e.target.value }))}
                  rows={4}
                  placeholder="Optional context for the decision"
                />
              </div>
            </div>
          )}

          {responseType === 'message' && (
            <div className="flex flex-col gap-3">
              <div className="flex items-center justify-between gap-4 rounded-md border border-zinc-800 p-3">
                <Label htmlFor="cp-message-acknowledged" className="cursor-pointer">
                  Acknowledged
                </Label>
                <Switch
                  id="cp-message-acknowledged"
                  checked={form.messageAcknowledged}
                  onCheckedChange={(checked) => {
                    setForm((current) => ({ ...current, messageAcknowledged: checked }))
                  }}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="cp-message-reply">Reply</Label>
                <Textarea
                  id="cp-message-reply"
                  value={form.messageReply}
                  onChange={(e) => setForm((current) => ({ ...current, messageReply: e.target.value }))}
                  rows={4}
                  placeholder="Optional reply"
                />
              </div>
            </div>
          )}

          {!knownWorkflow && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cp-generic-response">Response *</Label>
              <Textarea
                id="cp-generic-response"
                value={form.genericResponse}
                onChange={(e) => setForm((current) => ({ ...current, genericResponse: e.target.value }))}
                placeholder="Enter your response"
                rows={4}
              />
            </div>
          )}

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cp-source-type">Responder source type *</Label>
              <Select
                value={responderType}
                onValueChange={(value) => {
                  if (value) setResponderType(value)
                }}
              >
                <SelectTrigger id="cp-source-type" className="h-8 w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {allowedResponderTypes.map((type) => (
                    <SelectItem key={type} value={type}>
                      {type}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
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
