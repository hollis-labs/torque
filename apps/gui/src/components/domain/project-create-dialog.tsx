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
import type { Project } from '@/lib/types'

interface ProjectCreateDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated?: (project: Project) => void
}

export function ProjectCreateDialog({
  open,
  onOpenChange,
  onCreated,
}: ProjectCreateDialogProps) {
  const api = useApi()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [repoPath, setRepoPath] = useState('')
  const [icon, setIcon] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open) {
      setName('')
      setDescription('')
      setRepoPath('')
      setIcon('')
    }
  }, [open])

  const trimmedName = name.trim()
  const trimmedRepoPath = repoPath.trim()
  const canSubmit = trimmedName.length > 0 && trimmedRepoPath.length > 0

  async function handleSubmit() {
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const project = await api.createProject({
        name: trimmedName,
        description: description.trim(),
        repo_path: trimmedRepoPath,
        icon: icon.trim(),
      })
      notifySuccess(`Created project ${project.name}`)
      onOpenChange(false)
      onCreated?.(project)
    } catch (err) {
      notifyError(err, 'Failed to create project')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New project</DialogTitle>
          <DialogDescription>
            Projects group sprints, epics, and tasks under a shared roadmap.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="proj-name">Name *</Label>
            <Input
              id="proj-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Project name"
              autoFocus
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="proj-description">Description</Label>
            <Textarea
              id="proj-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Optional description"
              rows={3}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="proj-repo-path">Project path *</Label>
            <Input
              id="proj-repo-path"
              value={repoPath}
              onChange={(e) => setRepoPath(e.target.value)}
              placeholder="/Users/you/Projects/my-app"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="proj-icon">Icon</Label>
            <Input
              id="proj-icon"
              value={icon}
              onChange={(e) => setIcon(e.target.value)}
              placeholder="AB"
              maxLength={4}
              className="w-24"
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit || submitting}>
            {submitting ? 'Creating…' : 'Create project'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
