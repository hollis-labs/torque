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
import { parseUrn } from '@/lib/messaging'
import { MESSAGE_KINDS, type MessageEnvelope, type MessageKind } from '@/lib/types'

interface ComposeMessageDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Pre-fill the sender URN (e.g. the inbox address being viewed). */
  defaultFrom?: string
  /** Pre-fill the recipient URN. */
  defaultTo?: string
  /** Pre-select the message kind. */
  defaultKind?: MessageKind
  onSent?: (env: MessageEnvelope) => void
}

/** True for a non-empty string that parses as a canonical messaging URN. */
function isValidUrn(v: string): boolean {
  return v.trim() !== '' && parseUrn(v) !== null
}

/**
 * Compose and send a new messaging Envelope. Subject + body are folded into
 * the payload object the messaging GUI projects back out on the other side.
 */
export function ComposeMessageDialog({
  open,
  onOpenChange,
  defaultFrom = '',
  defaultTo = '',
  defaultKind = 'notice',
  onSent,
}: ComposeMessageDialogProps) {
  const api = useApi()
  const [from, setFrom] = useState(defaultFrom)
  const [to, setTo] = useState(defaultTo)
  const [kind, setKind] = useState<MessageKind>(defaultKind)
  const [channel, setChannel] = useState('')
  const [subject, setSubject] = useState('')
  const [body, setBody] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open) {
      setFrom(defaultFrom)
      setTo(defaultTo)
      setKind(defaultKind)
      setChannel('')
      setSubject('')
      setBody('')
    }
  }, [open, defaultFrom, defaultTo, defaultKind])

  const fromOk = isValidUrn(from)
  const toOk = isValidUrn(to)
  const canSend = fromOk && toOk && body.trim() !== '' && !submitting

  async function handleSend() {
    if (!canSend) return
    setSubmitting(true)
    try {
      const payload: Record<string, string> = { body: body.trim() }
      if (subject.trim()) payload.subject = subject.trim()
      const env = await api.sendMessage({
        kind,
        from: from.trim(),
        to: to.trim(),
        channel: channel.trim() || undefined,
        payload,
        content_type: 'application/json',
      })
      notifySuccess('Message sent')
      onOpenChange(false)
      onSent?.(env)
    } catch (err) {
      notifyError(err, 'Failed to send message')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Compose message</DialogTitle>
          <DialogDescription>
            Send an envelope through the messaging store. Addresses are
            canonical URNs: <code>msg://&lt;kind&gt;/&lt;authority&gt;/&lt;id&gt;</code>.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="compose-from">From</Label>
            <Input
              id="compose-from"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              placeholder="msg://user/local/operator"
              aria-invalid={from !== '' && !fromOk}
              className="font-mono text-xs"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="compose-to">To</Label>
            <Input
              id="compose-to"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              placeholder="msg://agent/local/orchestrator"
              aria-invalid={to !== '' && !toOk}
              className="font-mono text-xs"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label>Kind</Label>
              <Select
                value={kind}
                onValueChange={(v: unknown) => {
                  if (typeof v === 'string') setKind(v as MessageKind)
                }}
              >
                <SelectTrigger aria-label="Message kind" size="sm" className="h-8 w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {MESSAGE_KINDS.map((k) => (
                    <SelectItem key={k} value={k}>
                      {k}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="compose-channel">Channel</Label>
              <Input
                id="compose-channel"
                value={channel}
                onChange={(e) => setChannel(e.target.value)}
                placeholder="optional"
              />
            </div>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="compose-subject">Subject</Label>
            <Input
              id="compose-subject"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder="Optional one-line subject"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="compose-body">Message</Label>
            <Textarea
              id="compose-body"
              value={body}
              onChange={(e) => setBody(e.target.value)}
              placeholder="Write your message…"
              rows={5}
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={handleSend} disabled={!canSend}>
            {submitting ? 'Sending…' : 'Send message'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
