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
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Tag } from '@/lib/types'

interface TagCreateDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated?: (tag: Tag) => void
}

export function TagCreateDialog({ open, onOpenChange, onCreated }: TagCreateDialogProps) {
  const api = useApi()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open) {
      setName('')
      setDescription('')
    }
  }, [open])

  const trimmedName = name.trim()
  const canSubmit = trimmedName.length > 0

  async function handleSubmit() {
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const tag = await api.createTag({
        name: trimmedName,
        description: description.trim(),
      })
      notifySuccess(`Created tag ${tag.name}`)
      onOpenChange(false)
      onCreated?.(tag)
    } catch (err) {
      notifyError(err, 'Failed to create tag')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New tag</DialogTitle>
          <DialogDescription>
            Add a tag directly from the task filter bar.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tag-name">Name *</Label>
            <Input
              id="tag-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Tag name"
              autoFocus
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tag-description">Description</Label>
            <Textarea
              id="tag-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Optional description"
              rows={3}
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit || submitting}>
            {submitting ? 'Creating…' : 'Create tag'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
