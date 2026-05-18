import { useMemo } from 'react'
import { Trash2, Plus } from 'lucide-react'
import { Input, Textarea, Button, CollapsibleSection } from '@hollis-labs/sysop-ui'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@hollis-labs/sysop-ui'
import { formatDurationMs, formatTokenBudget } from '@/lib/sentinel-display'
import { UNLIMITED, type Task } from '@/lib/types'

interface ExecutionContextProps {
  task: Task
  editing: boolean
  draft: Task
  onDraftChange: <K extends keyof Task>(field: K, value: Task[K]) => void
}

export function ExecutionContext({ task, editing, draft, onDraftChange }: ExecutionContextProps) {
  const source = editing ? draft : task

  const summary = useMemo(() => {
    const parts: string[] = []
    if (source.tools.length > 0) parts.push(`${source.tools.length} tools`)
    if (source.files.length > 0) parts.push(`${source.files.length} files`)
    const envCount = Object.keys(source.environment).length
    if (envCount > 0) parts.push(`${envCount} env`)
    return parts.join(' · ') || 'empty'
  }, [source])

  return (
    <CollapsibleSection label="Execution Context" accent="violet" summary={summary}>
      <div className="flex flex-col gap-4">
        {/* system_prompt */}
        <FieldRow label="System Prompt">
          {editing ? (
            <Textarea
              value={source.system_prompt}
              onChange={(e) => onDraftChange('system_prompt', e.target.value)}
              rows={4}
              className="text-[13px]"
            />
          ) : source.system_prompt ? (
            <pre className="whitespace-pre-wrap text-[13px] text-zinc-300 font-sans">
              {source.system_prompt}
            </pre>
          ) : (
            <Empty />
          )}
        </FieldRow>

        {/* tools */}
        <FieldRow label="Tools">
          {editing ? (
            <StringListInput
              value={source.tools}
              onChange={(v) => onDraftChange('tools', v)}
              placeholder="comma-separated (e.g. bash, git, grep)"
            />
          ) : source.tools.length > 0 ? (
            <ChipList items={source.tools} />
          ) : (
            <Empty />
          )}
        </FieldRow>

        {/* files */}
        <FieldRow label="Files">
          {editing ? (
            <StringListInput
              value={source.files}
              onChange={(v) => onDraftChange('files', v)}
              placeholder="comma-separated file paths"
            />
          ) : source.files.length > 0 ? (
            <ChipList items={source.files} mono />
          ) : (
            <Empty />
          )}
        </FieldRow>

        {/* environment */}
        <FieldRow label="Environment">
          {editing ? (
            <EnvEditor
              value={source.environment}
              onChange={(v) => onDraftChange('environment', v)}
            />
          ) : Object.keys(source.environment).length > 0 ? (
            <EnvTable value={source.environment} />
          ) : (
            <Empty />
          )}
        </FieldRow>

        {/* max_duration_ms */}
        <FieldRow label="Max Duration">
          {editing ? (
            <NullableSentinelInput
              value={source.max_duration_ms}
              onChange={(v) => onDraftChange('max_duration_ms', v)}
            />
          ) : (
            <SentinelText display={formatDurationMs(source.max_duration_ms)} />
          )}
        </FieldRow>

        {/* token_budget */}
        <FieldRow label="Token Budget">
          {editing ? (
            <NullableSentinelInput
              value={source.token_budget}
              onChange={(v) => onDraftChange('token_budget', v)}
            />
          ) : (
            <SentinelText display={formatTokenBudget(source.token_budget)} />
          )}
        </FieldRow>
      </div>
    </CollapsibleSection>
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

function ChipList({ items, mono }: { items: string[]; mono?: boolean }) {
  return (
    <div className="flex flex-wrap gap-1">
      {items.map((item, i) => (
        <span
          key={`${item}-${i}`}
          className={`px-1.5 py-0.5 rounded border border-zinc-800 bg-zinc-900 text-[11px] text-zinc-300 ${mono ? 'font-mono' : ''}`}
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
  const textValue = value.join(', ')
  return (
    <Input
      value={textValue}
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

function EnvTable({ value }: { value: Record<string, string> }) {
  const entries = Object.entries(value)
  return (
    <div className="flex flex-col gap-1">
      {entries.map(([k, v]) => (
        <div key={k} className="grid grid-cols-[140px_1fr] gap-2 text-[12px] font-mono">
          <span className="text-zinc-500 truncate">{k}</span>
          <span className="text-zinc-300 truncate">{v}</span>
        </div>
      ))}
    </div>
  )
}

function EnvEditor({
  value,
  onChange,
}: {
  value: Record<string, string>
  onChange: (v: Record<string, string>) => void
}) {
  const entries = Object.entries(value)

  function update(idx: number, key: string, val: string) {
    const next: Record<string, string> = {}
    entries.forEach(([k, v], i) => {
      if (i === idx) {
        if (key !== '') next[key] = val
      } else {
        next[k] = v
      }
    })
    onChange(next)
  }

  function addRow() {
    onChange({ ...value, '': '' })
  }

  function removeRow(idx: number) {
    const next: Record<string, string> = {}
    entries.forEach(([k, v], i) => {
      if (i !== idx) next[k] = v
    })
    onChange(next)
  }

  return (
    <div className="flex flex-col gap-1">
      {entries.map(([k, v], idx) => (
        <div key={idx} className="grid grid-cols-[1fr_1fr_auto] gap-2">
          <Input
            value={k}
            onChange={(e) => update(idx, e.target.value, v)}
            placeholder="KEY"
            className="h-7 text-[12px] font-mono"
          />
          <Input
            value={v}
            onChange={(e) => update(idx, k, e.target.value)}
            placeholder="value"
            className="h-7 text-[12px] font-mono"
          />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => removeRow(idx)}
            className="h-7 w-7 p-0"
            aria-label={`Remove ${k || 'row'}`}
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
        <Plus className="h-3 w-3 mr-1" /> Add
      </Button>
    </div>
  )
}

function SentinelText({ display }: { display: ReturnType<typeof formatDurationMs> }) {
  const cls =
    display.mode === 'default'
      ? 'italic text-zinc-600'
      : display.mode === 'unlimited'
        ? 'font-mono text-zinc-400'
        : 'text-zinc-300'
  return <span className={`text-[13px] ${cls}`}>{display.label}</span>
}

function NullableSentinelInput({
  value,
  onChange,
}: {
  value: number | null
  onChange: (v: number | null) => void
}) {
  const mode: 'default' | 'unlimited' | 'value' =
    value === null ? 'default' : value === UNLIMITED ? 'unlimited' : 'value'
  return (
    <div className="flex items-center gap-2">
      <Select
        value={mode}
        onValueChange={(m) => {
          if (m === 'default') onChange(null)
          else if (m === 'unlimited') onChange(UNLIMITED)
          else onChange(1)
        }}
      >
        <SelectTrigger className="h-7 w-28 text-[12px]">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="default">default</SelectItem>
          <SelectItem value="unlimited">unlimited</SelectItem>
          <SelectItem value="value">value</SelectItem>
        </SelectContent>
      </Select>
      {mode === 'value' && (
        <Input
          type="number"
          min={0}
          value={value ?? 0}
          onChange={(e) => onChange(Number(e.target.value))}
          className="h-7 w-28 text-[13px]"
        />
      )}
    </div>
  )
}
