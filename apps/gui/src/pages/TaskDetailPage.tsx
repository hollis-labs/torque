import { useState, useEffect } from 'react'
import { useParams, Link } from 'react-router-dom'
import { ArrowLeft, FolderOpen } from 'lucide-react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { StatusBadge } from '@/components/domain/status-badge'
import { PriorityBadge } from '@/components/domain/priority-badge'
import { CommentList } from '@/components/domain/comment-list'
import { RunCard } from '@/components/domain/run-card'
import { EmptyState } from '@/components/domain/empty-state'
import { TagChip } from '@/components/domain/tag-chip'
import { useApi } from '@/hooks/use-api'
import { TASK_STATUSES } from '@/lib/constants'
import type { Task, Run, Comment, Artifact, TaskStatus } from '@/lib/types'

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

export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()

  const [task, setTask] = useState<Task | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Tab data — lazy loaded
  const [comments, setComments] = useState<Comment[] | null>(null)
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [artifacts, setArtifacts] = useState<Artifact[] | null>(null)
  const [activeTab, setActiveTab] = useState('comments')

  useEffect(() => {
    if (!id) return
    setLoading(true)
    api.getTask(id).then((t) => {
      setTask(t)
      setError(null)
    }).catch((err: Error) => {
      setError(err.message)
    }).finally(() => {
      setLoading(false)
    })
  }, [api, id])

  useEffect(() => {
    if (!id || !task) return
    if (activeTab === 'comments' && comments === null) {
      api.listComments(id).then(setComments).catch(() => setComments([]))
    }
    if (activeTab === 'runs' && runs === null) {
      api.listRuns(id).then(setRuns).catch(() => setRuns([]))
    }
    if (activeTab === 'artifacts' && artifacts === null) {
      api.listArtifacts(id).then(setArtifacts).catch(() => setArtifacts([]))
    }
  }, [activeTab, id, task, comments, runs, artifacts, api])

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
      // no-op
    }
  }

  if (loading) {
    return (
      <div className="p-6 flex flex-col gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-5 w-96" />
        <div className="flex gap-2">
          <Skeleton className="h-6 w-24 rounded-full" />
          <Skeleton className="h-6 w-12 rounded-full" />
        </div>
        <Skeleton className="h-40 w-full rounded-lg" />
      </div>
    )
  }

  if (error || !task) {
    return (
      <div className="p-6">
        <EmptyState variant="error" description={error ?? 'Task not found.'} />
      </div>
    )
  }

  const nextStatuses = TASK_STATUSES.filter((s) => s !== task.status).slice(0, 4)

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="border-b border-border bg-card px-6 py-4">
        <div className="flex items-center gap-2 mb-3">
          <Link
            to="/"
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            <ArrowLeft className="h-3.5 w-3.5" />
            Board
          </Link>
          <span className="text-muted-foreground/40 text-xs">/</span>
          <span className="text-xs text-muted-foreground font-mono">{task.id}</span>
        </div>

        <div className="flex items-start justify-between gap-4">
          <div className="flex flex-col gap-2 min-w-0">
            <h1 className="text-xl font-semibold text-foreground leading-tight">{task.title}</h1>
            <div className="flex items-center gap-2 flex-wrap">
              <StatusBadge status={task.status} />
              <PriorityBadge priority={task.priority} />
              {task.tags.map((tag) => (
                <TagChip key={tag.slug} tag={tag} />
              ))}
            </div>
          </div>

          {/* Transition buttons */}
          <div className="flex flex-wrap gap-1.5 shrink-0">
            {nextStatuses.map((s) => (
              <Button
                key={s}
                variant="outline"
                size="sm"
                className="text-xs h-7"
                onClick={() => handleTransition(s)}
              >
                {TRANSITION_LABELS[s] ?? s}
              </Button>
            ))}
          </div>
        </div>
      </div>

      <div className="flex flex-1 overflow-hidden">
        {/* Main content */}
        <div className="flex-1 overflow-auto p-6">
          <Tabs value={activeTab} onValueChange={setActiveTab}>
            <TabsList className="mb-4">
              <TabsTrigger value="comments">Comments</TabsTrigger>
              <TabsTrigger value="runs">Runs</TabsTrigger>
              <TabsTrigger value="artifacts">Artifacts</TabsTrigger>
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
                  {Array.from({ length: 2 }).map((_, i) => <Skeleton key={i} className="h-24 w-full rounded-lg" />)}
                </div>
              ) : runs.length === 0 ? (
                <EmptyState variant="no-results" title="No runs yet" description="This task hasn't been executed yet." />
              ) : (
                <div className="flex flex-col gap-3">
                  {runs.map((run) => <RunCard key={run.id} run={run} />)}
                </div>
              )}
            </TabsContent>

            <TabsContent value="artifacts">
              {artifacts === null ? (
                <Skeleton className="h-24 w-full rounded-lg" />
              ) : artifacts.length === 0 ? (
                <EmptyState variant="no-results" title="No artifacts" description="No artifacts have been produced for this task." />
              ) : (
                <div className="flex flex-col gap-3">
                  {artifacts.map((artifact) => (
                    <Card key={artifact.id}>
                      <CardHeader>
                        <CardTitle className="text-sm font-medium flex items-center gap-2">
                          <FolderOpen className="h-4 w-4 text-muted-foreground" />
                          {artifact.type}
                        </CardTitle>
                      </CardHeader>
                      <CardContent>
                        {artifact.file_path && (
                          <p className="text-xs font-mono text-muted-foreground">{artifact.file_path}</p>
                        )}
                        {artifact.url && (
                          <a href={artifact.url} className="text-xs text-primary hover:underline" target="_blank" rel="noopener noreferrer">
                            {artifact.url}
                          </a>
                        )}
                        {artifact.content && (
                          <pre className="mt-2 text-xs bg-muted rounded p-2 overflow-auto max-h-40 whitespace-pre-wrap">
                            {artifact.content}
                          </pre>
                        )}
                      </CardContent>
                    </Card>
                  ))}
                </div>
              )}
            </TabsContent>
          </Tabs>
        </div>

        {/* Side details */}
        <aside className="w-64 shrink-0 border-l border-border bg-card p-4 overflow-auto">
          <h2 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-3">Details</h2>
          <Separator className="mb-3" />

          <dl className="flex flex-col gap-3 text-sm">
            <div>
              <dt className="text-xs text-muted-foreground mb-0.5">Executor</dt>
              <dd className="font-medium text-foreground">{task.executor || <span className="italic text-muted-foreground">none</span>}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground mb-0.5">Agent Profile</dt>
              <dd className="font-medium text-foreground">{task.agent_profile || <span className="italic text-muted-foreground">default</span>}</dd>
            </div>
            {task.working_dir && (
              <div>
                <dt className="text-xs text-muted-foreground mb-0.5">Working Dir</dt>
                <dd className="font-mono text-xs text-foreground break-all">{task.working_dir}</dd>
              </div>
            )}
            {task.cost_budget !== null && task.cost_budget !== undefined && (
              <div>
                <dt className="text-xs text-muted-foreground mb-0.5">Cost Budget</dt>
                <dd className="font-medium text-foreground">${task.cost_budget.toFixed(2)}</dd>
              </div>
            )}
            {task.description && (
              <>
                <Separator />
                <div>
                  <dt className="text-xs text-muted-foreground mb-1">Description</dt>
                  <dd className="text-sm text-foreground whitespace-pre-wrap">{task.description}</dd>
                </div>
              </>
            )}
          </dl>
        </aside>
      </div>
    </div>
  )
}
