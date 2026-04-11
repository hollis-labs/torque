import { useState, useEffect, useCallback } from 'react'
import { useParams, useSearchParams, Link } from 'react-router-dom'
import { FolderOpen } from 'lucide-react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { Skeleton } from '@/components/ui/skeleton'
import { CommentList } from '@/components/domain/comment-list'
import { RunCard } from '@/components/domain/run-card'
import { EmptyState } from '@/components/domain/empty-state'
import { TaskDetailHeader } from '@/components/domain/task-detail-header'
import { DetailSection } from '@/components/domain/detail-section'
import { TaskProperties } from '@/components/domain/task-properties'
import { ExecutionContext } from '@/components/domain/execution-context'
import { LifecycleRules } from '@/components/domain/lifecycle-rules'
import { DeliverablesAndDeps } from '@/components/domain/deliverables-deps'
import { useApi } from '@/hooks/use-api'
import { computeTaskDiff } from '@/lib/task-diff'
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
  const [searchParams, setSearchParams] = useSearchParams()
  const editing = searchParams.get('edit') === '1'

  const [task, setTask] = useState<Task | null>(null)
  const [draft, setDraft] = useState<Task | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  // Tab data — lazy loaded
  const [comments, setComments] = useState<Comment[] | null>(null)
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [artifacts, setArtifacts] = useState<Artifact[] | null>(null)
  const [activeTab, setActiveTab] = useState('comments')

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

  // Initialize/reset draft when editing starts, or clear when it ends
  useEffect(() => {
    if (editing && task) {
      setDraft((prev) => prev ?? { ...task })
    } else {
      setDraft(null)
      setSaveError(null)
    }
  }, [editing, task])

  // Load container lists once on mount. Used for both view-mode name
  // resolution (linked project/sprint/epic) and edit-mode pickers.
  useEffect(() => {
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
  }, [api])

  // Lazy-load tab content (view mode only)
  const taskLoaded = task !== null
  useEffect(() => {
    if (!id || !taskLoaded || editing) return
    if (activeTab === 'comments' && comments === null) {
      api.listComments(id).then(setComments).catch(() => setComments([]))
    }
    if (activeTab === 'runs' && runs === null) {
      api.listRuns(id).then(setRuns).catch(() => setRuns([]))
    }
    if (activeTab === 'artifacts' && artifacts === null) {
      api.listArtifacts(id).then(setArtifacts).catch(() => setArtifacts([]))
    }
  }, [activeTab, id, taskLoaded, editing, comments, runs, artifacts, api])

  const updateDraft = useCallback(
    <K extends keyof Task>(field: K, value: Task[K]) => {
      setDraft((prev) => (prev ? { ...prev, [field]: value } : prev))
    },
    [],
  )

  async function handleAddComment(content: string) {
    if (!id) return
    const comment = await api.addComment(id, content)
    setComments((prev) => [...(prev ?? []), comment])
  }

  async function handleTransition(status: TaskStatus) {
    if (!id) return
    try {
      const updated = await api.transitionTask(id, status)
      setTask(updated)
    } catch {
      // no-op — the badge doesn't change, user sees no update
    }
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
        onDraftChange={updateDraft}
        onTransition={handleTransition}
        onEdit={handleEdit}
        onSave={handleSave}
        onCancel={handleCancel}
      />

      {/* Save error banner */}
      {saveError && (
        <div className="border-b border-red-900/50 bg-red-950/40 px-4 py-2">
          <span className="text-[12px] text-red-300">{saveError}</span>
        </div>
      )}

      {/* Field sections */}
      <div className="flex-1 overflow-auto">
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

          {/* Description — inline, zinc accent, always open */}
          <DetailSection label="Description" accent="zinc" collapsible={false}>
            {editing ? (
              <Textarea
                value={displayDraft.description}
                onChange={(e) => updateDraft('description', e.target.value)}
                rows={4}
                className="text-[13px]"
                placeholder="Task description"
              />
            ) : task.description ? (
              <p className="text-[13px] text-zinc-300 whitespace-pre-wrap">{task.description}</p>
            ) : (
              <span className="text-[13px] italic text-zinc-600">—</span>
            )}
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

        {/* Activity zone — hidden in edit mode */}
        {!editing && (
          <div className="border-t border-zinc-800/80 px-4 py-3">
            <Tabs value={activeTab} onValueChange={setActiveTab}>
              <TabsList className="bg-transparent border-b border-zinc-800/50 rounded-none p-0 h-auto mb-3">
                <TabsTrigger
                  value="comments"
                  className="text-[10px] uppercase tracking-[.18em] data-[state=active]:border-b data-[state=active]:border-zinc-100 data-[state=active]:text-zinc-100 text-zinc-500 rounded-none bg-transparent px-3 py-1.5"
                >
                  Comments
                </TabsTrigger>
                <TabsTrigger
                  value="runs"
                  className="text-[10px] uppercase tracking-[.18em] data-[state=active]:border-b data-[state=active]:border-zinc-100 data-[state=active]:text-zinc-100 text-zinc-500 rounded-none bg-transparent px-3 py-1.5"
                >
                  Runs
                </TabsTrigger>
                <TabsTrigger
                  value="artifacts"
                  className="text-[10px] uppercase tracking-[.18em] data-[state=active]:border-b data-[state=active]:border-zinc-100 data-[state=active]:text-zinc-100 text-zinc-500 rounded-none bg-transparent px-3 py-1.5"
                >
                  Artifacts
                </TabsTrigger>
              </TabsList>

              <TabsContent value="comments">
                <CommentList
                  comments={comments ?? []}
                  loading={comments === null}
                  onAddComment={handleAddComment}
                />
              </TabsContent>

              <TabsContent value="runs">
                {runs === null ? (
                  <div className="flex flex-col gap-3">
                    {Array.from({ length: 2 }).map((_, i) => (
                      <Skeleton key={i} className="h-24 w-full rounded-md" />
                    ))}
                  </div>
                ) : runs.length === 0 ? (
                  <EmptyState
                    variant="no-results"
                    title="No runs yet"
                    description="This task hasn't been executed yet."
                  />
                ) : (
                  <div className="flex flex-col gap-3">
                    {runs.map((run) => (
                      <RunCard key={run.id} run={run} />
                    ))}
                  </div>
                )}
              </TabsContent>

              <TabsContent value="artifacts">
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
                      <div
                        key={artifact.id}
                        className="rounded-md border border-zinc-800/50 p-3"
                      >
                        <div className="flex items-center gap-2 mb-1">
                          <FolderOpen className="h-3 w-3 text-zinc-500" />
                          <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
                            {artifact.type}
                          </span>
                        </div>
                        {artifact.file_path && (
                          <p className="text-[11px] font-mono text-zinc-400 break-all">
                            {artifact.file_path}
                          </p>
                        )}
                        {artifact.url && (
                          <Link
                            to={artifact.url}
                            className="text-[11px] text-blue-400 hover:text-blue-300 break-all"
                            target="_blank"
                            rel="noopener noreferrer"
                          >
                            {artifact.url}
                          </Link>
                        )}
                        {artifact.content && (
                          <pre className="mt-2 text-[11px] bg-zinc-900/60 rounded p-2 overflow-auto max-h-40 whitespace-pre-wrap text-zinc-300">
                            {artifact.content}
                          </pre>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </TabsContent>
            </Tabs>
          </div>
        )}
      </div>
    </div>
  )
}
