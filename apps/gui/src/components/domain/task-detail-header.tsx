import { Link } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { StatusBadge } from './status-badge'
import { PriorityBadge } from './priority-badge'
import { TagChip } from './tag-chip'
import { CopyableId } from './copyable-id'
import { TASK_STATUSES, PRIORITIES } from '@/lib/constants'
import type { Task, TaskStatus, Tag } from '@/lib/types'

const TRANSITION_LABELS: Partial<Record<TaskStatus, string>> = {
  todo: 'Mark To Do',
  queued: 'Queue',
  doing: 'Start',
  review: 'Send for Review',
  done: 'Mark Done',
  blocked: 'Block',
  paused: 'Pause',
  archived: 'Archive',
}

interface TaskDetailHeaderProps {
  task: Task
  editing: boolean
  draft: Task
  saving: boolean
  onDraftChange: <K extends keyof Task>(field: K, value: Task[K]) => void
  onTransition: (status: TaskStatus) => void
  onEdit: () => void
  onSave: () => void
  onCancel: () => void
}

export function TaskDetailHeader({
  task,
  editing,
  draft,
  saving,
  onDraftChange,
  onTransition,
  onEdit,
  onSave,
  onCancel,
}: TaskDetailHeaderProps) {
  const nextStatuses = TASK_STATUSES.filter((s) => s !== task.status).slice(0, 4)
  const source = editing ? draft : task

  return (
    <div className="border-b border-zinc-800/80 bg-zinc-950 px-4 py-3">
      {/* Breadcrumb */}
      <div className="flex items-center gap-2 mb-2">
        <Link
          to="/"
          className="flex items-center gap-1 text-[10px] uppercase tracking-[.18em] text-zinc-500 hover:text-zinc-300 transition-colors"
        >
          <ArrowLeft className="h-3 w-3" />
          Board
        </Link>
        <span className="text-zinc-700 text-[10px]">/</span>
        <CopyableId id={task.id} />
      </div>

      {/* Title + badges + actions row */}
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-2 min-w-0 flex-1">
          {editing ? (
            <Input
              value={source.title}
              onChange={(e) => onDraftChange('title', e.target.value)}
              placeholder="Task title"
              className="h-8 text-base font-semibold tracking-[.02em] bg-zinc-900 border-zinc-800"
            />
          ) : (
            <h1 className="text-base font-semibold tracking-[.02em] text-zinc-100 leading-tight">
              {task.title}
            </h1>
          )}

          <div className="flex items-center gap-2 flex-wrap">
            <StatusBadge status={task.status} />
            <CopyableId id={task.id} />
            {editing ? (
              <Select
                value={String(source.priority)}
                onValueChange={(v) => {
                  if (v !== null) onDraftChange('priority', Number(v))
                }}
              >
                <SelectTrigger className="h-6 w-20 text-[11px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PRIORITIES.map((p) => (
                    <SelectItem key={p.value} value={String(p.value)}>
                      {p.label} ({p.description})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <PriorityBadge priority={task.priority} />
            )}
            {editing ? (
              <TagsInput
                value={source.tags}
                onChange={(v) => onDraftChange('tags', v)}
              />
            ) : (
              task.tags.map((tag) => <TagChip key={tag.slug} tag={tag} />)
            )}
          </div>
        </div>

        {/* Action buttons */}
        <div className="flex flex-wrap gap-1.5 shrink-0">
          {editing ? (
            <>
              <Button
                size="sm"
                onClick={onSave}
                disabled={saving || !source.title.trim()}
                className="text-[11px] h-7 uppercase tracking-[.18em]"
              >
                {saving ? 'Saving...' : 'Save'}
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={onCancel}
                disabled={saving}
                className="text-[11px] h-7 uppercase tracking-[.18em]"
              >
                Cancel
              </Button>
            </>
          ) : (
            <>
              {nextStatuses.map((s) => (
                <Button
                  key={s}
                  variant="outline"
                  size="sm"
                  className="text-[11px] h-7 uppercase tracking-[.18em]"
                  onClick={() => onTransition(s)}
                >
                  {TRANSITION_LABELS[s] ?? s}
                </Button>
              ))}
              <Button
                size="sm"
                variant="outline"
                onClick={onEdit}
                className="text-[11px] h-7 uppercase tracking-[.18em]"
              >
                Edit
              </Button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

/**
 * Simple comma-separated tag name input. On change, parses
 * the text into Tag[] stubs. The API auto-resolves/creates tags by name
 * on save, so we only need to track names here.
 */
function TagsInput({
  value,
  onChange,
}: {
  value: Tag[]
  onChange: (v: Tag[]) => void
}) {
  return (
    <Input
      value={value.map((t) => t.name).join(', ')}
      onChange={(e) => {
        const names = e.target.value
          .split(',')
          .map((s) => s.trim())
          .filter((s) => s.length > 0)
        // Build Tag[] stubs — the diff helper reads names only.
        const tags: Tag[] = names.map((name) => ({
          slug: name.toLowerCase().replace(/\s+/g, '-'),
          name,
          description: '',
          color: 'zinc',
          created_at: '',
          updated_at: '',
        }))
        onChange(tags)
      }}
      placeholder="comma-separated tags"
      className="h-6 w-64 text-[11px]"
    />
  )
}
