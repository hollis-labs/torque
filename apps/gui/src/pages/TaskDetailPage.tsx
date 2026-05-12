import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useParams, useSearchParams, useNavigate } from 'react-router-dom'
import { useActiveRun } from '@/hooks/active-runs-context'
import { Plus } from 'lucide-react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { CommentList } from '@/components/domain/comment-list'
import { EmptyState } from '@/components/domain/empty-state'
import { ArtifactCard } from '@/components/domain/artifact-card'
import { ActivityTimeline } from '@/components/domain/activity-timeline'
import { SubtodosPanel } from '@/components/domain/subtodos-panel'
import { DebugTabStub } from '@/components/domain/debug-tab-stub'
import {
  AttachArtifactDialog,
  type AttachArtifactPayload,
} from '@/components/domain/attach-artifact-dialog'
import { TaskDetailHeader } from '@/components/domain/task-detail-header'
import { ParentPlanLink } from '@/components/domain/parent-plan-link'
import { BlockedReasonAlert } from '@/components/domain/blocked-reason-alert'
import { DetailSection } from '@/components/domain/detail-section'
import { TaskProperties } from '@/components/domain/task-properties'
import { TaskFacets } from '@/components/domain/task-facets'
import { ExecutionContext } from '@/components/domain/execution-context'
import { LifecycleRules } from '@/components/domain/lifecycle-rules'
import { DeliverablesAndDeps } from '@/components/domain/deliverables-deps'
import { SendBackDialog } from '@/components/domain/send-back-dialog'
import { ActivityPanel } from '@/components/domain/activity-panel'
import { TaskCheckpointsBanner } from '@/components/domain/task-checkpoints-banner'
import { TaskHITLRequestDialog } from '@/components/domain/task-hitl-request-dialog'
import { useApi } from '@/hooks/use-api'
import { useArrowNav } from '@/hooks/use-arrow-nav'
import { hasBlockedReason } from '@/lib/blocked-reason'
import { computeTaskDiff } from '@/lib/task-diff'
import { readTaskListCursor } from '@/lib/task-list-cursor'
import { notifyError, notifySuccess } from '@/lib/toast'
import type {
  Task,
  Run,
  Comment,
  Artifact,
  TaskStatus,
  Project,
  Sprint,
  Epic,
} from '@/lib/types'

export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const editing = searchParams.get('edit') === '1'

  const [task, setTask] = useState<Task | null>(null)
  const [draft, setDraft] = useState<Task | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  const [queueBusy, setQueueBusy] = useState(false)
  const [sendBackOpen, setSendBackOpen] = useState(false)
  const [attachOpen, setAttachOpen] = useState(false)
  const [hitlRequestOpen, setHitlRequestOpen] = useState(false)
  const [hitlRequestBusy, setHitlRequestBusy] = useState(false)
  const [checkpointBannerKey, setCheckpointBannerKey] = useState(0)

  // Tab data — lazy loaded
  const [comments, setComments] = useState<Comment[] | null>(null)
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [artifacts, setArtifacts] = useState<Artifact[] | null>(null)
  const [activeTab, setActiveTab] = useState('details')

  // Container picker options (loaded when edit mode activates)
  const [projects, setProjects] = useState<Project[]>([])
  const [sprints, setSprints] = useState<Sprint[]>([])
  const [epics, setEpics] = useState<Epic[]>([])
  const [pickersLoading, setPickersLoading] = useState(false)

  // Fetch task on mount / id change
  useEffect(() => {
    if (!id) return
    setLoading(true)
    api
      .getTask(id)
      .then((t) => {
        setTask(t)
        setError(null)
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false))
  }, [api, id])

  // Refetch the task when an in-flight run finishes so the header stats
  // (turns / tokens / cost) roll forward without requiring a hard reload.
  // Drop-to-null is the SSE run.finished signal from ActiveRunsProvider.
  const activeRun = useActiveRun(id)
  const hadActiveRun = useRef(false)
  useEffect(() => {
    if (activeRun) {
      hadActiveRun.current = true
      return
    }
    if (!hadActiveRun.current || !id) return
    hadActiveRun.current = false
    api.getTask(id).then(setTask).catch(() => {})
    // Invalidate the runs cache so the Logs tab pulls the completed run
    // on next visit.
    setRuns(null)
  }, [activeRun, id, api])

  // Resolve the current task's position inside the last rendered list so
  // arrow keys jump to adjacent tasks. Missing cursor silently disables.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const cursor = useMemo(() => readTaskListCursor(), [id])
  const cursorIds = cursor.ids
  const cursorFilter = cursor.filter
  const cursorIndex = id ? cursorIds.indexOf(id) : -1

  const navigateToAdjacent = useCallback(
    (delta: -1 | 1) => {
      if (cursorIndex < 0 || cursorIds.length < 2) return
      const nextIndex = (cursorIndex + delta + cursorIds.length) % cursorIds.length
      navigate(`/tasks/${cursorIds[nextIndex]}`)
    },
    [cursorIndex, cursorIds, navigate],
  )

  useArrowNav({
    enabled: !editing && cursorIndex >= 0 && cursorIds.length > 1,
    onPrev: () => navigateToAdjacent(-1),
    onNext: () => navigateToAdjacent(1),
  })

  // Auto-advance when the user transitions this task into a status the
  // active cursor filter excludes (e.g. marks `done` while viewing a
  // todo|doing|review list). Walks forward through the cursor to the next
  // still-reachable task; if at the end, returns to /operations.
  const autoAdvanceAfterTransition = useCallback(
    (newStatus: TaskStatus) => {
      if (!cursorFilter || cursorFilter.statuses.length === 0) return
      if (cursorFilter.statuses.includes(newStatus)) return
      if (cursorIndex < 0) return
      const nextId = cursorIds[cursorIndex + 1]
      if (nextId) {
        navigate(`/tasks/${nextId}`)
      } else {
        navigate('/operations')
      }
    },
    [cursorFilter, cursorIds, cursorIndex, navigate],
  )

  // Initialize/reset draft when editing starts, or clear when it ends
  useEffect(() => {
    if (editing && task) {
      setDraft((prev) => prev ?? { ...task })
    } else {
      setDraft(null)
      setSaveError(null)
    }
  }, [editing, task])

  // View-mode name resolution: fetch only the individual container entities
  // referenced by the current task, so links render real names instead of
  // raw IDs. No-op when the task has no containers assigned.
  useEffect(() => {
    if (!task) return
    if (task.project_id) {
      api
        .getProject(task.project_id)
        .then((p) =>
          setProjects((prev) => (prev.some((x) => x.id === p.id) ? prev : [...prev, p])),
        )
        .catch(() => {})
    }
    if (task.sprint_id) {
      api
        .getSprint(task.sprint_id)
        .then((s) =>
          setSprints((prev) => (prev.some((x) => x.id === s.id) ? prev : [...prev, s])),
        )
        .catch(() => {})
    }
    if (task.epic_id) {
      api
        .getEpic(task.epic_id)
        .then((e) =>
          setEpics((prev) => (prev.some((x) => x.id === e.id) ? prev : [...prev, e])),
        )
        .catch(() => {})
    }
  }, [task, api])

  // Edit-mode picker loading: fetch the full container lists only when
  // entering edit mode (drives the project/sprint/epic <Select> options).
  useEffect(() => {
    if (!editing) return
    setPickersLoading(true)
    Promise.allSettled([
      api.listProjects(),
      api.listSprints(),
      api.listEpics(),
    ]).then(([pRes, sRes, eRes]) => {
      if (pRes.status === 'fulfilled') setProjects(pRes.value.projects)
      if (sRes.status === 'fulfilled') setSprints(sRes.value.sprints)
      if (eRes.status === 'fulfilled') setEpics(eRes.value.epics)
      setPickersLoading(false)
    })
  }, [editing, api])

  // Lazy-load tab content (view mode only)
  const taskLoaded = task !== null
  useEffect(() => {
    if (!id || !taskLoaded || editing) return
    if (activeTab === 'comments' && comments === null) {
      api.listComments('task', id).then(setComments).catch(() => setComments([]))
    }
    if (activeTab === 'logs') {
      // Timeline unions runs, comments, and artifacts — load any that are
      // still missing so the view is coherent on first render.
      if (runs === null) {
        api.listRuns(id).then(setRuns).catch(() => setRuns([]))
      }
      if (comments === null) {
        api.listComments('task', id).then(setComments).catch(() => setComments([]))
      }
      if (artifacts === null) {
        api.listArtifacts(id).then(setArtifacts).catch(() => setArtifacts([]))
      }
    }
    if (activeTab === 'artifacts' && artifacts === null) {
      api.listArtifacts(id).then(setArtifacts).catch(() => setArtifacts([]))
    }
  }, [activeTab, id, taskLoaded, editing, comments, runs, artifacts, api])

  // Seed draft from task if the user's first edit lands before the init
  // effect has run. Without this, the brief window between entering edit
  // mode and the draft-init effect committing can silently drop the first
  // keystroke.
  const updateDraft = useCallback(
    <K extends keyof Task>(field: K, value: Task[K]) => {
      setDraft((prev) => {
        const baseDraft = prev ?? (editing && task ? { ...task } : null)
        return baseDraft ? { ...baseDraft, [field]: value } : baseDraft
      })
    },
    [editing, task],
  )

  async function handleAddComment(content: string) {
    if (!id) return
    try {
      const comment = await api.addComment('task', id, content)
      setComments((prev) => [...(prev ?? []), comment])
    } catch (err) {
      notifyError(err, 'Failed to add comment')
    }
  }

  async function handleAttachArtifact(payload: AttachArtifactPayload) {
    if (!id) return
    await api.createArtifact({ task_id: id, ...payload })
    // Refetch the list so the new row picks up server-generated fields
    // (id, created_at, normalized metadata envelope).
    const fresh = await api.listArtifacts(id)
    setArtifacts(fresh)
    notifySuccess('Artifact attached')
  }

  async function handleOpenHITLRequest() {
    if (!id) return
    if (artifacts !== null) {
      setHitlRequestOpen(true)
      return
    }

    setHitlRequestBusy(true)
    try {
      setArtifacts(await api.listArtifacts(id))
    } catch (err) {
      notifyError(err, 'Failed to load artifacts for checkpoint prefill')
    } finally {
      setHitlRequestBusy(false)
      setHitlRequestOpen(true)
    }
  }

  function handleCheckpointRequested() {
    setCheckpointBannerKey((key) => key + 1)
    if (id) {
      api.getTask(id).then(setTask).catch(() => {})
    }
  }

  async function handleDeleteArtifact(artifactId: number) {
    try {
      await api.deleteArtifact(artifactId)
      setArtifacts((prev) => (prev ? prev.filter((a) => a.id !== artifactId) : prev))
      notifySuccess('Artifact deleted')
    } catch (err) {
      notifyError(err, 'Failed to delete artifact')
      throw err
    }
  }

  async function handleTransition(status: TaskStatus) {
    if (!id) return
    try {
      const updated = await api.transitionTask(id, status)
      setTask(updated)
      autoAdvanceAfterTransition(updated.status)
    } catch (err) {
      notifyError(err, 'Failed to update task status')
    }
  }

  // Flip the manual flag so the scheduler picks up (manual:false) or
  // releases (manual:true) the task. Status is driven automatically by
  // the scheduler once manual=false, so we never touch status here.
  async function handleQueueToggle() {
    if (!id || !task) return
    const next = !task.manual
    setQueueBusy(true)
    try {
      const updated = await api.updateTask(id, { manual: next })
      setTask(updated)
      notifySuccess(next ? 'Unqueued' : 'Queued')
      if (updated.status !== task.status) autoAdvanceAfterTransition(updated.status)
    } catch (err) {
      notifyError(err, 'Failed to update queue state')
    } finally {
      setQueueBusy(false)
    }
  }

  // Re-queue a reviewed task with written feedback. Order matters: the
  // comment lands first so the re-dispatched agent sees the feedback; only
  // then does status flip so the scheduler re-queues.
  async function handleSendBack(feedback: string) {
    if (!id || !task) return
    const content = `[user feedback]\n\n${feedback}`
    const comment = await api.addComment('task', id, content, 'user')
    if (task.blocked_reason) {
      try {
        await api.updateTask(id, { blocked_reason: '' })
      } catch {
        // non-fatal — transition still clears the review park
      }
    }
    const updated = await api.transitionTask(id, 'todo')
    setTask(updated)
    // Only append locally if the comments pane has already lazy-loaded.
    // If it hasn't, leave null so the next tab switch fetches a fresh list
    // that already includes this comment.
    setComments((prev) => (prev === null ? prev : [...prev, comment]))
    notifySuccess('Sent back to todo')
    autoAdvanceAfterTransition(updated.status)
  }

  function handleEdit() {
    setSearchParams({ edit: '1' })
  }

  function handleCancel() {
    setSaveError(null)
    setSearchParams({})
  }

  async function handleSave() {
    if (!id || !task || !draft) return
    const diff = computeTaskDiff(task, draft)
    if (Object.keys(diff).length === 0) {
      // Nothing changed — just exit edit mode without flashing the Saving state
      setSearchParams({})
      return
    }
    setSaving(true)
    setSaveError(null)
    try {
      const updated = await api.updateTask(id, diff)
      setTask(updated)
      setDraft(null)
      setSearchParams({})
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="p-4 flex flex-col gap-3">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-5 w-96" />
        <Skeleton className="h-32 w-full rounded-md" />
        <Skeleton className="h-16 w-full rounded-md" />
        <Skeleton className="h-16 w-full rounded-md" />
      </div>
    )
  }

  if (error || !task) {
    return (
      <div className="p-4">
        <EmptyState variant="error" description={error ?? 'Task not found.'} />
      </div>
    )
  }

  // In edit mode, draft should be set — fall back to task to keep TS happy
  const displayDraft = draft ?? task

  return (
    <div className="flex h-full flex-col">
      <TaskDetailHeader
        task={task}
        editing={editing}
        draft={displayDraft}
        saving={saving}
        queueBusy={queueBusy}
        checkpointRequestBusy={hitlRequestBusy}
        onDraftChange={updateDraft}
        onTransition={handleTransition}
        onQueueToggle={handleQueueToggle}
        onRequestCheckpoint={handleOpenHITLRequest}
        onEdit={handleEdit}
        onSave={handleSave}
        onCancel={handleCancel}
        onSendBack={() => setSendBackOpen(true)}
        onTaskChange={(next) => {
          setTask((prev) => {
            if (prev && prev.status !== next.status) {
              autoAdvanceAfterTransition(next.status)
            }
            return next
          })
        }}
        onTaskDelete={() => navigate('/operations')}
      />

      {!editing && <ParentPlanLink task={task} />}

      <SendBackDialog
        open={sendBackOpen}
        onOpenChange={setSendBackOpen}
        onSubmit={handleSendBack}
      />

      <AttachArtifactDialog
        open={attachOpen}
        onOpenChange={setAttachOpen}
        onSubmit={handleAttachArtifact}
      />

      <TaskHITLRequestDialog
        task={task}
        artifacts={artifacts ?? []}
        open={hitlRequestOpen}
        onOpenChange={setHitlRequestOpen}
        onRequested={handleCheckpointRequested}
      />

      {/* Blocked / paused reason — agent's last word before the wheels stopped */}
      {!editing && hasBlockedReason(task) && (
        <BlockedReasonAlert
          status={task.status as 'blocked' | 'paused'}
          reason={task.blocked_reason}
          className="border-x-0 border-t-0"
        />
      )}

      {/* Pending checkpoints banner — inline respond/cancel */}
      {!editing && id && <TaskCheckpointsBanner key={checkpointBannerKey} taskId={id} />}

      {/* Live activity panel — visible while the task has an active run. */}
      {!editing && id && <ActivityPanel taskId={id} />}

      {/* Save error banner */}
      {saveError && (
        <div className="border-b border-red-900/50 bg-red-950/40 px-4 py-2">
          <span className="text-[12px] text-red-300">{saveError}</span>
        </div>
      )}

      {/* Content area — edit mode shows the form flat, view mode tabs everything. */}
      <div className="flex-1 overflow-auto">
        {editing ? (
          <div className="flex flex-col gap-3 p-4">
            <TaskProperties
              task={task}
              editing={editing}
              draft={displayDraft}
              onDraftChange={updateDraft}
              projects={projects}
              sprints={sprints}
              epics={epics}
              pickersLoading={pickersLoading}
            />

            <DetailSection label="Description" accent="zinc" collapsible={false}>
              <Textarea
                value={displayDraft.description}
                onChange={(e) => updateDraft('description', e.target.value)}
                rows={4}
                className="text-[13px]"
                placeholder="Task description"
              />
            </DetailSection>

            <ExecutionContext
              task={task}
              editing={editing}
              draft={displayDraft}
              onDraftChange={updateDraft}
            />

            <LifecycleRules
              task={task}
              editing={editing}
              draft={displayDraft}
              onDraftChange={updateDraft}
            />

            <DeliverablesAndDeps
              task={task}
              editing={editing}
              draft={displayDraft}
              onDraftChange={updateDraft}
            />
          </div>
        ) : (
          <Tabs value={activeTab} onValueChange={setActiveTab}>
            <TabsList className="bg-transparent border-b border-zinc-800/80 rounded-none p-0 h-auto px-4 pt-2 justify-start gap-0 flex-none">
              {([
                ['details', 'Details'],
                ['comments', 'Comments'],
                ['artifacts', 'Artifacts'],
                ['subtodos', 'Sub-todos'],
                ['logs', 'Logs'],
                ['debug', 'Debug'],
              ] as const).map(([value, label]) => (
                <TabsTrigger
                  key={value}
                  value={value}
                  className="text-[10px] uppercase tracking-[.18em] data-[state=active]:border-b data-[state=active]:border-zinc-100 data-[state=active]:text-zinc-100 text-zinc-500 rounded-none bg-transparent px-3 py-1.5"
                >
                  {label}
                </TabsTrigger>
              ))}
            </TabsList>

            <TabsContent value="details" className="px-4 py-3">
              <div className="flex flex-col gap-3">
                <TaskProperties
                  task={task}
                  editing={false}
                  draft={displayDraft}
                  onDraftChange={updateDraft}
                  projects={projects}
                  sprints={sprints}
                  epics={epics}
                  pickersLoading={pickersLoading}
                />

                <TaskFacets task={task} />

                <DetailSection label="Description" accent="zinc" collapsible={false}>
                  {task.description ? (
                    <p className="text-[13px] text-zinc-300 whitespace-pre-wrap">{task.description}</p>
                  ) : (
                    <span className="text-[13px] italic text-zinc-600">—</span>
                  )}
                </DetailSection>

                <ExecutionContext
                  task={task}
                  editing={false}
                  draft={displayDraft}
                  onDraftChange={updateDraft}
                />

                <LifecycleRules
                  task={task}
                  editing={false}
                  draft={displayDraft}
                  onDraftChange={updateDraft}
                />

                <DeliverablesAndDeps
                  task={task}
                  editing={false}
                  draft={displayDraft}
                  onDraftChange={updateDraft}
                />
              </div>
            </TabsContent>

            <TabsContent value="comments" className="px-4 py-3">
              <CommentList
                comments={comments ?? []}
                loading={comments === null}
                onAddComment={handleAddComment}
              />
            </TabsContent>

            <TabsContent value="artifacts" className="px-4 py-3">
              <div className="flex flex-col gap-3">
                <div className="flex justify-end">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setAttachOpen(true)}
                    className="h-7 gap-1.5 text-[11px]"
                  >
                    <Plus className="h-3 w-3" aria-hidden />
                    Attach artifact
                  </Button>
                </div>
                {artifacts === null ? (
                  <Skeleton className="h-24 w-full rounded-md" />
                ) : artifacts.length === 0 ? (
                  <EmptyState
                    variant="no-results"
                    title="No artifacts"
                    description="No artifacts have been produced for this task."
                  />
                ) : (
                  <div className="flex flex-col gap-3">
                    {artifacts.map((artifact) => (
                      <ArtifactCard
                        key={artifact.id}
                        artifact={artifact}
                        onDelete={handleDeleteArtifact}
                      />
                    ))}
                  </div>
                )}
              </div>
            </TabsContent>

            <TabsContent value="subtodos" className="px-4 py-3">
              {id && (
                <SubtodosPanel
                  taskId={id}
                  subtodos={task.subtodos ?? []}
                  onChange={(next) =>
                    setTask((prev) => (prev ? { ...prev, subtodos: next } : prev))
                  }
                />
              )}
            </TabsContent>

            <TabsContent value="logs" className="px-4 py-3">
              {id && (
                <ActivityTimeline
                  taskId={id}
                  runs={runs}
                  comments={comments}
                  artifacts={artifacts}
                />
              )}
            </TabsContent>

            <TabsContent value="debug" className="px-4 py-3">
              <DebugTabStub />
            </TabsContent>
          </Tabs>
        )}
      </div>
    </div>
  )
}
