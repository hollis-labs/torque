import { useCallback, useEffect, useMemo, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Plus, ArrowLeft, Play, Trash2 } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@hollis-labs/sysop-ui'
import { Skeleton, Button, Input, Label, Textarea, PageHeader, EmptyState, CopyableId } from '@hollis-labs/sysop-ui'
import { StatusBadge } from '@/components/domain/status-badge'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { PhaseRollup, PlanDetail, PlanPhase, Task } from '@/lib/types'

// canExecute reports whether the Execute Plan button is enabled for a
// plan in the named status. Mirrors planstart.startableStatus on the
// backend — todo and review only. doing/done/blocked/abandoned are
// handled out-of-band (idempotency check; terminal-not-rerunnable in V0).
function canExecute(status: string): boolean {
  return status === 'todo' || status === 'review'
}

// PlanDetailPage renders a plan as a header strip + Kanban-ish phase columns.
// Columns are indexed by PlanPhase.id; children are bucketed via
// metadata.phase_id (see docs/plans-v1.md). Adding a task opens the local
// dialog which creates it with parent_id + metadata.phase_id pre-filled.
export default function PlanDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const api = useApi()

  const [plan, setPlan] = useState<PlanDetail | null>(null)
  const [children, setChildren] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [addPhaseOpen, setAddPhaseOpen] = useState(false)
  const [addTaskOpen, setAddTaskOpen] = useState<PlanPhase | null>(null)
  // CW-20260503-0017 (S2.1) — Execute Plan button. busy disables the
  // button while the request is in flight; otherwise enabled state is
  // derived from plan.task.status (todo / review eligible).
  const [executing, setExecuting] = useState(false)

  const load = useCallback(async () => {
    if (!id) return
    try {
      const [detail, kids] = await Promise.all([api.getPlan(id), api.listPlanChildren(id)])
      setPlan(detail)
      setChildren(kids.tasks ?? [])
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load plan')
    } finally {
      setLoading(false)
    }
  }, [api, id])

  useEffect(() => {
    setLoading(true)
    load()
  }, [load])

  const phaseColumns = useMemo(() => {
    if (!plan) return []
    return [...plan.plan.phases].sort((a, b) => a.order - b.order)
  }, [plan])

  function bucket(phaseID: string): Task[] {
    return children.filter((c) => {
      const md = c.metadata ?? {}
      const pid = typeof md['phase_id'] === 'string' ? (md['phase_id'] as string) : ''
      return pid === phaseID
    })
  }

  function unbucketed(): Task[] {
    return children.filter((c) => {
      const md = c.metadata ?? {}
      const pid = typeof md['phase_id'] === 'string' ? (md['phase_id'] as string) : ''
      return pid === ''
    })
  }

  const handleExecute = useCallback(async () => {
    if (!id || executing) return
    setExecuting(true)
    try {
      const res = await api.startPlan(id)
      notifySuccess(`Orchestrator session ${res.session_id} started.`)
      // The orchestrator session view lives at /sessions/{id} in the
      // long-running view; for V0 we just reload the plan to surface
      // the new metadata.plan.orchestrator_session_id and the doing
      // status badge. A dedicated session view is post-MVP.
      load()
    } catch (err) {
      notifyError(err, 'Failed to start plan')
    } finally {
      setExecuting(false)
    }
  }, [api, executing, id, load])

  if (loading) {
    return (
      <div className="flex h-full flex-col">
        <PageHeader title="Plan" />
        <div className="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-48 w-full rounded-md" />
          ))}
        </div>
      </div>
    )
  }

  if (error || !plan) {
    return (
      <div className="flex h-full flex-col">
        <PageHeader title="Plan" />
        <EmptyState variant="error" title="Something went wrong" description={error ?? 'Plan not found.'} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Plans">
        <Button variant="ghost" size="sm" onClick={() => navigate('/plans')} className="gap-1">
          <ArrowLeft className="h-3.5 w-3.5" />
          All plans
        </Button>
      </PageHeader>

      {/* Header strip: title + id + progress */}
      <div className="border-b border-zinc-800/80 bg-zinc-950 px-4 py-3">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <h2 className="truncate text-lg font-semibold text-zinc-100">{plan.task.title}</h2>
            <div className="mt-1 flex items-center gap-2">
              <CopyableId id={plan.task.id} />
              <StatusBadge status={plan.task.status} />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant="default"
              disabled={executing || !canExecute(plan.task.status)}
              title={
                canExecute(plan.task.status)
                  ? 'Boot an orchestrator session and walk this plan'
                  : `Cannot execute a plan in status=${plan.task.status}`
              }
              onClick={() => void handleExecute()}
              className="gap-1"
            >
              <Play className="h-3.5 w-3.5" />
              {executing ? 'Starting…' : 'Execute Plan'}
            </Button>
            <Button size="sm" onClick={() => setAddPhaseOpen(true)} className="gap-1">
              <Plus className="h-3.5 w-3.5" />
              Add phase
            </Button>
          </div>
        </div>
        {plan.task.description ? (
          <p className="mt-2 whitespace-pre-wrap text-sm text-zinc-400">{plan.task.description}</p>
        ) : null}
        <ProgressSummary progress={plan.progress} />
      </div>

      {/* Phase columns */}
      <div className="flex-1 overflow-auto">
        <div className="flex h-full min-h-0 flex-row gap-3 overflow-x-auto p-4">
          {phaseColumns.map((phase) => (
            <PhaseColumn
              key={phase.id}
              phase={phase}
              rollup={plan.progress.by_phase[phase.id]}
              tasks={bucket(phase.id)}
              onAddTask={() => setAddTaskOpen(phase)}
              onTaskClick={(t) => navigate(`/tasks/${t.id}`)}
              onRemovePhase={async () => {
                try {
                  await api.removePlanPhase(plan.task.id, phase.id)
                  notifySuccess(`Removed phase ${phase.name}`)
                  load()
                } catch (err) {
                  notifyError(err, 'Failed to remove phase')
                }
              }}
            />
          ))}
          {unbucketed().length > 0 && (
            <UnbucketedColumn tasks={unbucketed()} onTaskClick={(t) => navigate(`/tasks/${t.id}`)} />
          )}
        </div>
      </div>

      <AddPhaseDialog
        open={addPhaseOpen}
        onOpenChange={setAddPhaseOpen}
        onSubmit={async (name, acceptance) => {
          try {
            await api.addPlanPhase(plan.task.id, { name, acceptance })
            notifySuccess(`Added phase ${name}`)
            setAddPhaseOpen(false)
            load()
          } catch (err) {
            notifyError(err, 'Failed to add phase')
          }
        }}
      />

      <AddTaskDialog
        phase={addTaskOpen}
        onOpenChange={(open) => setAddTaskOpen(open ? addTaskOpen : null)}
        onSubmit={async (title, description) => {
          if (!addTaskOpen) return
          try {
            await api.createTask({
              title,
              description,
              parent_id: plan.task.id,
              metadata: { phase_id: addTaskOpen.id },
              project_id: plan.task.project_id ?? undefined,
              sprint_id: plan.task.sprint_id ?? undefined,
            } as unknown as Partial<Omit<Task, 'tags'>> & { tags?: string[] })
            notifySuccess(`Created task under ${addTaskOpen.name}`)
            setAddTaskOpen(null)
            load()
          } catch (err) {
            notifyError(err, 'Failed to create task')
          }
        }}
      />
    </div>
  )
}

function ProgressSummary({ progress }: { progress: PlanDetail['progress'] }) {
  const total = progress.total_children
  const pct = total === 0 ? 0 : Math.round((progress.done / total) * 100)
  return (
    <div className="mt-3 flex items-center gap-3 text-[11px] text-zinc-400">
      <span>
        <span className="font-medium text-zinc-200">{progress.done}</span>/{total} done
      </span>
      <span>
        <span className="font-medium text-amber-400">{progress.blocked}</span> blocked
      </span>
      <div className="h-1 flex-1 overflow-hidden rounded-full bg-zinc-800">
        <div className="h-full bg-emerald-500 transition-all" style={{ width: `${pct}%` }} />
      </div>
      <span className="w-10 text-right font-mono text-[10px] text-zinc-500">{pct}%</span>
    </div>
  )
}

function PhaseColumn({
  phase,
  rollup,
  tasks,
  onAddTask,
  onTaskClick,
  onRemovePhase,
}: {
  phase: PlanPhase
  rollup?: PhaseRollup
  tasks: Task[]
  onAddTask: () => void
  onTaskClick: (task: Task) => void
  onRemovePhase: () => void
}) {
  const total = rollup?.total ?? 0
  const done = rollup?.done ?? 0
  return (
    <div className="flex w-72 shrink-0 flex-col gap-2 rounded-md border border-zinc-800 bg-zinc-900/30 p-2">
      <div className="flex items-start justify-between gap-2 px-1 pt-1">
        <div className="min-w-0">
          <div className="truncate text-[11px] font-semibold uppercase tracking-[.18em] text-zinc-300">
            {phase.name}
          </div>
          <div className="mt-0.5 flex items-center gap-2 text-[10px] text-zinc-500">
            <span className="rounded bg-zinc-950 px-1 font-mono">{phase.id}</span>
            <span>
              {done}/{total} done
            </span>
          </div>
        </div>
        <button
          type="button"
          onClick={onRemovePhase}
          aria-label={`Remove phase ${phase.name}`}
          className="rounded p-1 text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>
      {phase.acceptance ? (
        <div className="rounded bg-zinc-950 px-2 py-1 text-[10px] text-zinc-500">
          {phase.acceptance}
        </div>
      ) : null}
      <div className="flex flex-col gap-2">
        {tasks.length === 0 ? (
          <div className="rounded border border-dashed border-zinc-800 px-2 py-4 text-center text-[11px] text-zinc-500">
            No tasks yet
          </div>
        ) : (
          tasks.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => onTaskClick(t)}
              className="flex flex-col gap-1 rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-left transition-colors hover:border-zinc-700 hover:bg-zinc-900"
            >
              <div className="truncate text-[12px] font-medium text-zinc-100">{t.title}</div>
              <div className="flex items-center justify-between gap-2">
                <StatusBadge status={t.status} className="py-0.5 text-[9px]" />
                <span className="truncate font-mono text-[9px] text-zinc-500">{t.id}</span>
              </div>
            </button>
          ))
        )}
      </div>
      <Button size="sm" variant="outline" onClick={onAddTask} className="mt-auto gap-1">
        <Plus className="h-3.5 w-3.5" />
        Add task
      </Button>
    </div>
  )
}

function UnbucketedColumn({
  tasks,
  onTaskClick,
}: {
  tasks: Task[]
  onTaskClick: (t: Task) => void
}) {
  return (
    <div className="flex w-72 shrink-0 flex-col gap-2 rounded-md border border-dashed border-zinc-800 bg-zinc-900/10 p-2">
      <div className="px-1 pt-1 text-[11px] font-semibold uppercase tracking-[.18em] text-zinc-500">
        Unassigned
      </div>
      <div className="flex flex-col gap-2">
        {tasks.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => onTaskClick(t)}
            className="flex flex-col gap-1 rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-left hover:bg-zinc-900"
          >
            <div className="truncate text-[12px] font-medium text-zinc-100">{t.title}</div>
            <div className="flex items-center justify-between gap-2">
              <StatusBadge status={t.status} className="py-0.5 text-[9px]" />
              <span className="truncate font-mono text-[9px] text-zinc-500">{t.id}</span>
            </div>
          </button>
        ))}
      </div>
    </div>
  )
}

function AddPhaseDialog({
  open,
  onOpenChange,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onSubmit: (name: string, acceptance: string) => Promise<void>
}) {
  const [name, setName] = useState('')
  const [acceptance, setAcceptance] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (open) {
      setName('')
      setAcceptance('')
    }
  }, [open])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Add phase</DialogTitle>
          <DialogDescription>Append a phase to this plan.</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="phase-name">Name *</Label>
            <Input
              id="phase-name"
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Integration"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="phase-acceptance">Acceptance</Label>
            <Textarea
              id="phase-acceptance"
              rows={3}
              value={acceptance}
              onChange={(e) => setAcceptance(e.target.value)}
              placeholder="Optional acceptance criteria"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button>
          <Button
            disabled={!name.trim() || busy}
            onClick={async () => {
              setBusy(true)
              try {
                await onSubmit(name.trim(), acceptance.trim())
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? 'Adding…' : 'Add phase'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function AddTaskDialog({
  phase,
  onOpenChange,
  onSubmit,
}: {
  phase: PlanPhase | null
  onOpenChange: (open: boolean) => void
  onSubmit: (title: string, description: string) => Promise<void>
}) {
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (phase) {
      setTitle('')
      setDescription('')
    }
  }, [phase])

  return (
    <Dialog open={phase !== null} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Add task to {phase?.name ?? 'phase'}</DialogTitle>
          <DialogDescription>
            Pre-fills parent_id + metadata.phase_id so this task rolls up under the plan.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="task-title">Title *</Label>
            <Input
              id="task-title"
              autoFocus
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="e.g. Migration 015"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="task-desc">Description</Label>
            <Textarea
              id="task-desc"
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Optional"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button>
          <Button
            disabled={!title.trim() || busy}
            onClick={async () => {
              setBusy(true)
              try {
                await onSubmit(title.trim(), description.trim())
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? 'Creating…' : 'Create task'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
