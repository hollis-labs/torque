import { useState, type FormEvent } from 'react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@hollis-labs/sysop-ui'
import { Button, Input, Textarea, Label } from '@hollis-labs/sysop-ui'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@hollis-labs/sysop-ui'

const ARTIFACT_TYPES = [
  'note',
  'diff',
  'test-results',
  'screenshot',
  'pr-link',
  'branch',
  'commit',
  'log',
  'finding',
  'report',
  'metrics',
  'custom',
] as const

export interface AttachArtifactPayload {
  type: string
  file_path?: string
  url?: string
  content?: string
}

interface AttachArtifactDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (payload: AttachArtifactPayload) => Promise<void>
}

export function AttachArtifactDialog({
  open,
  onOpenChange,
  onSubmit,
}: AttachArtifactDialogProps) {
  const [type, setType] = useState<string>('note')
  const [filePath, setFilePath] = useState('')
  const [url, setUrl] = useState('')
  const [note, setNote] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showValidation, setShowValidation] = useState(false)

  // At least one of file_path, url, or note must be provided — otherwise the
  // record is a naked type tag with no payload and the server rejects it as
  // noise.
  const hasPayload =
    filePath.trim() !== '' || url.trim() !== '' || note.trim() !== ''

  function reset() {
    setType('note')
    setFilePath('')
    setUrl('')
    setNote('')
    setError(null)
    setShowValidation(false)
  }

  function handleOpenChange(next: boolean) {
    if (submitting) return
    if (!next) reset()
    onOpenChange(next)
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (!hasPayload) {
      setShowValidation(true)
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      const payload: AttachArtifactPayload = { type }
      if (filePath.trim()) payload.file_path = filePath.trim()
      if (url.trim()) payload.url = url.trim()
      if (note.trim()) payload.content = note.trim()
      await onSubmit(payload)
      reset()
      onOpenChange(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to attach artifact')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Attach artifact</DialogTitle>
            <DialogDescription>
              Attach a link, file path, or short note to this task so agents
              and reviewers can find it later.
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-2">
              <Label
                htmlFor="attach-type"
                className="text-[11px] uppercase tracking-[.18em] text-zinc-400"
              >
                Type
              </Label>
              <Select
                value={type}
                onValueChange={(v) => {
                  if (v !== null) setType(v)
                }}
              >
                <SelectTrigger id="attach-type" className="h-8 w-full text-sm">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ARTIFACT_TYPES.map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="flex flex-col gap-2">
              <Label
                htmlFor="attach-file-path"
                className="text-[11px] uppercase tracking-[.18em] text-zinc-400"
              >
                File path
              </Label>
              <Input
                id="attach-file-path"
                value={filePath}
                onChange={(e) => setFilePath(e.target.value)}
                placeholder="/absolute/path/to/file"
                disabled={submitting}
                autoComplete="off"
                className="font-mono text-[12px]"
              />
            </div>

            <div className="flex flex-col gap-2">
              <Label
                htmlFor="attach-url"
                className="text-[11px] uppercase tracking-[.18em] text-zinc-400"
              >
                URL
              </Label>
              <Input
                id="attach-url"
                type="url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://…"
                disabled={submitting}
                autoComplete="off"
                className="text-[12px]"
              />
            </div>

            <div className="flex flex-col gap-2">
              <Label
                htmlFor="attach-note"
                className="text-[11px] uppercase tracking-[.18em] text-zinc-400"
              >
                Note
              </Label>
              <Textarea
                id="attach-note"
                value={note}
                onChange={(e) => setNote(e.target.value)}
                rows={3}
                placeholder="Optional freeform text stored as content."
                disabled={submitting}
                className="resize-none text-sm"
              />
            </div>

            {showValidation && !hasPayload && (
              <p className="text-[12px] text-destructive">
                Provide at least one of file path, URL, or note.
              </p>
            )}
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
            <Button type="submit" disabled={submitting}>
              {submitting ? 'Attaching…' : 'Attach'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
