import { Link } from 'react-router-dom'
import { BookOpen, Calendar, Folder } from 'lucide-react'
import { Input, Switch, CollapsibleSection, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@hollis-labs/sysop-ui'
import { FilterEntityCombobox } from '@hollis-labs/sysop-ui/data'
import { formatCostBudget } from '@/lib/sentinel-display'
import { UNLIMITED, type Task, type Project, type Sprint, type Epic } from '@/lib/types'

interface TaskPropertiesProps {
  task: Task
  editing: boolean
  draft: Task
  onDraftChange: <K extends keyof Task>(field: K, value: Task[K]) => void
  projects: Project[]
  sprints: Sprint[]
  epics: Epic[]
  pickersLoading: boolean
}

export function TaskProperties({
  task,
  editing,
  draft,
  onDraftChange,
  projects,
  sprints,
  epics,
  pickersLoading,
}: TaskPropertiesProps) {
  const source = editing ? draft : task

  return (
    <CollapsibleSection label="Properties" accent="blue" collapsible={false}>
      <dl className="grid grid-cols-3 gap-x-4 gap-y-3">
        <Field label="Executor">
          {editing ? (
            <Input
              value={source.executor}
              onChange={(e) => onDraftChange('executor', e.target.value)}
              className="h-7 text-[13px]"
            />
          ) : (
            <PlainValue>{source.executor || '—'}</PlainValue>
          )}
        </Field>

        <Field label="Agent Profile">
          {editing ? (
            <Input
              value={source.agent_profile}
              onChange={(e) => onDraftChange('agent_profile', e.target.value)}
              className="h-7 text-[13px]"
            />
          ) : (
            <PlainValue>{source.agent_profile || '—'}</PlainValue>
          )}
        </Field>

        <Field label="Working Dir">
          {editing ? (
            <Input
              value={source.working_dir}
              onChange={(e) => onDraftChange('working_dir', e.target.value)}
              className="h-7 text-[13px] font-mono"
            />
          ) : (
            <MonoValue>{source.working_dir || '—'}</MonoValue>
          )}
        </Field>

        <Field label="Cost Budget">
          {editing ? (
            <SentinelInput
              value={source.cost_budget}
              onChange={(v) => onDraftChange('cost_budget', v)}
            />
          ) : (
            <SentinelValue display={formatCostBudget(source.cost_budget)} />
          )}
        </Field>

        <Field label="Max Retries">
          {editing ? (
            <Input
              type="number"
              min={0}
              value={source.max_retries}
              onChange={(e) => onDraftChange('max_retries', Number(e.target.value))}
              className="h-7 text-[13px]"
            />
          ) : (
            <PlainValue>{source.max_retries}</PlainValue>
          )}
        </Field>

        <Field label="Manual">
          {editing ? (
            <Switch
              checked={source.manual}
              onCheckedChange={(v) => onDraftChange('manual', v)}
            />
          ) : (
            <PlainValue>{source.manual ? 'Yes' : 'No'}</PlainValue>
          )}
        </Field>

        <Field label="Project">
          {editing ? (
            <FilterEntityCombobox
              icon={<Folder className="h-3.5 w-3.5" />}
              items={projects.map((p) => ({ id: p.id, name: p.name }))}
              value={source.project_id}
              onChange={(v) => onDraftChange('project_id', v)}
              allLabel={pickersLoading ? 'loading…' : 'none'}
              ariaLabel="Project"
            />
          ) : source.project_id ? (
            <LinkedValue to={`/projects/${source.project_id}`}>
              {projects.find((p) => p.id === source.project_id)?.name ?? source.project_id}
            </LinkedValue>
          ) : (
            <NoneValue />
          )}
        </Field>

        <Field label="Sprint">
          {editing ? (
            <FilterEntityCombobox
              icon={<Calendar className="h-3.5 w-3.5" />}
              items={sprints.map((s) => ({ id: s.id, name: s.name }))}
              value={source.sprint_id}
              onChange={(v) => onDraftChange('sprint_id', v)}
              allLabel={pickersLoading ? 'loading…' : 'none'}
              ariaLabel="Sprint"
            />
          ) : source.sprint_id ? (
            <LinkedValue to={`/sprints/${source.sprint_id}`}>
              {sprints.find((s) => s.id === source.sprint_id)?.name ?? source.sprint_id}
            </LinkedValue>
          ) : (
            <NoneValue />
          )}
        </Field>

        <Field label="Epic">
          {editing ? (
            <FilterEntityCombobox
              icon={<BookOpen className="h-3.5 w-3.5" />}
              items={epics.map((e) => ({ id: e.id, name: e.name }))}
              value={source.epic_id}
              onChange={(v) => onDraftChange('epic_id', v)}
              allLabel={pickersLoading ? 'loading…' : 'none'}
              ariaLabel="Epic"
            />
          ) : source.epic_id ? (
            <LinkedValue to={`/epics/${source.epic_id}`}>
              {epics.find((e) => e.id === source.epic_id)?.name ?? source.epic_id}
            </LinkedValue>
          ) : (
            <NoneValue />
          )}
        </Field>
      </dl>
    </CollapsibleSection>
  )
}

/* ----- small display primitives kept local to this file ----- */

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1 min-w-0">
      <dt className="text-[9px] uppercase tracking-[.18em] text-zinc-600">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  )
}

function PlainValue({ children }: { children: React.ReactNode }) {
  return <span className="text-[13px] text-zinc-300">{children}</span>
}

function MonoValue({ children }: { children: React.ReactNode }) {
  return <span className="text-[12px] font-mono text-zinc-300 break-all">{children}</span>
}

function NoneValue() {
  return <span className="text-[13px] italic text-zinc-600">none</span>
}

function LinkedValue({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <Link to={to} className="text-[13px] text-blue-400 hover:text-blue-300 truncate block">
      {children}
    </Link>
  )
}

function SentinelValue({ display }: { display: ReturnType<typeof formatCostBudget> }) {
  const cls =
    display.mode === 'default'
      ? 'italic text-zinc-600'
      : display.mode === 'unlimited'
        ? 'font-mono text-zinc-400'
        : 'text-zinc-300'
  return <span className={`text-[13px] ${cls}`}>{display.label}</span>
}

function SentinelInput({
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
          else onChange(0)
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
          step="0.01"
          min={0}
          value={value ?? 0}
          onChange={(e) => onChange(Number(e.target.value))}
          className="h-7 w-24 text-[13px]"
        />
      )}
    </div>
  )
}

