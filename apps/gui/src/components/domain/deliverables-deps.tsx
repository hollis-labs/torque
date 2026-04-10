import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { Trash2, Plus } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DetailSection } from './detail-section'
import type { Task, Deliverable, DeliverableType } from '@/lib/types'

const DELIVERABLE_TYPES: DeliverableType[] = [
  'diff',
  'test-results',
  'screenshot',
  'pr-link',
  'branch',
  'commit',
  'log',
  'finding',
  'report',
  'note',
  'metrics',
  'custom',
]

interface DeliverablesAndDepsProps {
  task: Task
  editing: boolean
  draft: Task
  onDraftChange: <K extends keyof Task>(field: K, value: Task[K]) => void
}

export function DeliverablesAndDeps({
  task,
  editing,
  draft,
  onDraftChange,
}: DeliverablesAndDepsProps) {
  const source = editing ? draft : task

  const summary = useMemo(() => {
    const parts: string[] = []
    if (source.deliverables.length > 0) parts.push(`${source.deliverables.length} deliverables`)
    if (source.depends_on.length > 0) parts.push(`${source.depends_on.length} deps`)
    return parts.join(' · ') || 'empty'
  }, [source.deliverables.length, source.depends_on.length])

  return (
    <DetailSection label="Deliverables & Dependencies" accent="red" summary={summary}>
      <div className="flex flex-col gap-4">
        <FieldRow label="Depends On">
          {editing ? (
            <StringListInput
              value={source.depends_on}
              onChange={(v) => onDraftChange('depends_on', v)}
              placeholder="comma-separated task IDs (e.g. tsk_abc, tsk_xyz)"
              mono
            />
          ) : source.depends_on.length > 0 ? (
            <div className="flex flex-wrap gap-1">
              {source.depends_on.map((id) => (
                <Link
                  key={id}
                  to={`/tasks/${id}`}
                  className="px-1.5 py-0.5 rounded border border-zinc-800 bg-zinc-900 text-[11px] font-mono text-blue-400 hover:text-blue-300"
                >
                  {id}
                </Link>
              ))}
            </div>
          ) : (
            <Empty />
          )}
        </FieldRow>

        <FieldRow label="Deliverable Preset">
          {editing ? (
            <Input
              value={source.deliverable_preset}
              onChange={(e) => onDraftChange('deliverable_preset', e.target.value)}
              placeholder="preset name"
              className="h-7 text-[13px]"
            />
          ) : source.deliverable_preset ? (
            <span className="text-[13px] text-zinc-300">{source.deliverable_preset}</span>
          ) : (
            <Empty />
          )}
        </FieldRow>

        <FieldRow label="Deliverables">
          {editing ? (
            <DeliverablesEditor
              value={source.deliverables}
              onChange={(v) => onDraftChange('deliverables', v)}
            />
          ) : source.deliverables.length > 0 ? (
            <DeliverablesTable value={source.deliverables} />
          ) : (
            <Empty />
          )}
        </FieldRow>
      </div>
    </DetailSection>
  )
}

/* ----- local primitives ----- */

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-4 items-start">
      <div className="text-[10px] uppercase tracking-[.18em] text-zinc-600 pt-1">{label}</div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

function Empty() {
  return <span className="text-[13px] italic text-zinc-600">—</span>
}

function StringListInput({
  value,
  onChange,
  placeholder,
  mono,
}: {
  value: string[]
  onChange: (v: string[]) => void
  placeholder: string
  mono?: boolean
}) {
  return (
    <Input
      value={value.join(', ')}
      onChange={(e) => {
        const items = e.target.value
          .split(',')
          .map((s) => s.trim())
          .filter((s) => s.length > 0)
        onChange(items)
      }}
      placeholder={placeholder}
      className={`h-7 text-[13px] ${mono ? 'font-mono' : ''}`}
    />
  )
}

function DeliverablesTable({ value }: { value: Deliverable[] }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="grid grid-cols-[120px_80px_1fr] gap-2 text-[9px] uppercase tracking-[.18em] text-zinc-600 pb-1 border-b border-zinc-800/50">
        <span>Type</span>
        <span>Required</span>
        <span>Description</span>
      </div>
      {value.map((d, i) => (
        <div key={i} className="grid grid-cols-[120px_80px_1fr] gap-2 text-[12px]">
          <span className="text-zinc-300">{d.type}</span>
          <span className="text-zinc-400">{d.required ? 'yes' : 'no'}</span>
          <span className="text-zinc-400 truncate">{d.description ?? '—'}</span>
        </div>
      ))}
    </div>
  )
}

function DeliverablesEditor({
  value,
  onChange,
}: {
  value: Deliverable[]
  onChange: (v: Deliverable[]) => void
}) {
  function updateRow(idx: number, patch: Partial<Deliverable>) {
    onChange(value.map((d, i) => (i === idx ? { ...d, ...patch } : d)))
  }
  function removeRow(idx: number) {
    onChange(value.filter((_, i) => i !== idx))
  }
  function addRow() {
    onChange([...value, { type: 'diff', required: false, description: '' }])
  }

  return (
    <div className="flex flex-col gap-2">
      {value.map((d, i) => (
        <div key={i} className="grid grid-cols-[140px_auto_1fr_auto] gap-2 items-center">
          <Select
            value={d.type}
            onValueChange={(v) => {
              if (v !== null) updateRow(i, { type: v as DeliverableType })
            }}
          >
            <SelectTrigger className="h-7 text-[12px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {DELIVERABLE_TYPES.map((t) => (
                <SelectItem key={t} value={t}>
                  {t}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Switch
            checked={d.required}
            onCheckedChange={(v) => updateRow(i, { required: v })}
            aria-label="Required"
          />
          <Input
            value={d.description ?? ''}
            onChange={(e) => updateRow(i, { description: e.target.value })}
            placeholder="description"
            className="h-7 text-[12px]"
          />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => removeRow(i)}
            className="h-7 w-7 p-0"
            aria-label="Remove deliverable"
          >
            <Trash2 className="h-3 w-3" />
          </Button>
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={addRow}
        className="h-7 text-[11px] uppercase tracking-[.18em] self-start"
      >
        <Plus className="h-3 w-3 mr-1" /> Add Deliverable
      </Button>
    </div>
  )
}
