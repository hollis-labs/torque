import { useMemo } from 'react'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DetailSection } from './detail-section'
import type { Task, OnDone, OnFail, OnReview, OnDoneMerge } from '@/lib/types'

const ON_DONE_OPTIONS: OnDone[] = ['close', 'review', 'notify']
const ON_FAIL_OPTIONS: OnFail[] = ['retry', 'block', 'escalate', 'notify']
const ON_REVIEW_OPTIONS: OnReview[] = ['pause', 'notify', 'auto-approve']
const ON_DONE_MERGE_OPTIONS: OnDoneMerge[] = ['none', 'auto', 'pr', 'auto-resolve']

interface LifecycleRulesProps {
  task: Task
  editing: boolean
  draft: Task
  onDraftChange: <K extends keyof Task>(field: K, value: Task[K]) => void
}

export function LifecycleRules({ task, editing, draft, onDraftChange }: LifecycleRulesProps) {
  const source = editing ? draft : task

  const summary = useMemo(
    () => [source.on_done, source.on_fail, source.on_review, source.on_done_merge].join(' · '),
    [source.on_done, source.on_fail, source.on_review, source.on_done_merge],
  )

  return (
    <DetailSection label="Lifecycle Rules" accent="amber" summary={summary}>
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-x-4 gap-y-3">
          <EnumField
            label="On Done"
            editing={editing}
            value={source.on_done}
            options={ON_DONE_OPTIONS}
            onChange={(v) => onDraftChange('on_done', v as OnDone)}
          />
          <EnumField
            label="On Fail"
            editing={editing}
            value={source.on_fail}
            options={ON_FAIL_OPTIONS}
            onChange={(v) => onDraftChange('on_fail', v as OnFail)}
          />
          <EnumField
            label="On Review"
            editing={editing}
            value={source.on_review}
            options={ON_REVIEW_OPTIONS}
            onChange={(v) => onDraftChange('on_review', v as OnReview)}
          />
          <EnumField
            label="On Done Merge"
            editing={editing}
            value={source.on_done_merge}
            options={ON_DONE_MERGE_OPTIONS}
            onChange={(v) => onDraftChange('on_done_merge', v as OnDoneMerge)}
          />
        </div>

        <FieldRow label="Escalation Chain">
          {editing ? (
            <StringListInput
              value={source.escalation_chain}
              onChange={(v) => onDraftChange('escalation_chain', v)}
              placeholder="comma-separated agent names"
            />
          ) : source.escalation_chain.length > 0 ? (
            <ChipList items={source.escalation_chain} />
          ) : (
            <Empty />
          )}
        </FieldRow>

        <FieldRow label="Quality Gates">
          {editing ? (
            <StringListInput
              value={source.quality_gates}
              onChange={(v) => onDraftChange('quality_gates', v)}
              placeholder="comma-separated gate names"
            />
          ) : source.quality_gates.length > 0 ? (
            <ChipList items={source.quality_gates} />
          ) : (
            <Empty />
          )}
        </FieldRow>

        <FieldRow label="Blocked Reason">
          {editing ? (
            <Textarea
              value={source.blocked_reason}
              onChange={(e) => onDraftChange('blocked_reason', e.target.value)}
              rows={2}
              className="text-[13px]"
            />
          ) : source.blocked_reason ? (
            <span className="text-[13px] text-zinc-300 whitespace-pre-wrap">
              {source.blocked_reason}
            </span>
          ) : (
            <Empty />
          )}
        </FieldRow>
      </div>
    </DetailSection>
  )
}

/* ----- local primitives ----- */

function EnumField({
  label,
  editing,
  value,
  options,
  onChange,
}: {
  label: string
  editing: boolean
  value: string
  options: readonly string[]
  onChange: (v: string) => void
}) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-[9px] uppercase tracking-[.18em] text-zinc-600">{label}</span>
      {editing ? (
        <Select value={value} onValueChange={(v) => { if (v !== null) onChange(v) }}>
          <SelectTrigger className="h-7 text-[12px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {options.map((opt) => (
              <SelectItem key={opt} value={opt}>
                {opt}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <span className="inline-flex w-fit px-2 py-0.5 rounded border border-zinc-800 bg-zinc-900 text-[11px] text-zinc-300 uppercase tracking-[.08em]">
          {value}
        </span>
      )}
    </div>
  )
}

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

function ChipList({ items }: { items: string[] }) {
  return (
    <div className="flex flex-wrap gap-1">
      {items.map((item, i) => (
        <span
          key={`${item}-${i}`}
          className="px-1.5 py-0.5 rounded border border-zinc-800 bg-zinc-900 text-[11px] text-zinc-300"
        >
          {item}
        </span>
      ))}
    </div>
  )
}

function StringListInput({
  value,
  onChange,
  placeholder,
}: {
  value: string[]
  onChange: (v: string[]) => void
  placeholder: string
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
      className="h-7 text-[13px]"
    />
  )
}
