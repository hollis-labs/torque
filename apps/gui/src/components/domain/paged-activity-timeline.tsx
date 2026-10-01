import { useApi } from '@/hooks/use-api'
import { usePagedList } from '@/hooks/use-paged-list'
import { ActivityTimeline } from './activity-timeline'
import { ListPageControls } from './list-page-controls'

/** Each source has its own server order and explicit continuation. */
export function PagedActivityTimeline({ taskId }: { taskId: string }) {
  const api = useApi()
  const runs = usePagedList({
    params: { task_id: taskId, sort_by: 'started_at' as const, sort_dir: 'desc' as const, include_total: true },
    fetchPage: ({ params, cursor, signal }) => api.pageRuns({ ...params, cursor }, signal),
    getId: row => row.id,
  })
  const comments = usePagedList({
    params: { taskId, sort_by: 'created_at', sort_dir: 'desc' as const, include_total: true },
    fetchPage: ({ params, cursor, signal }) => api.listComments('task', params.taskId, { sort_by: params.sort_by, sort_dir: params.sort_dir, include_total: params.include_total, cursor }, signal),
    getId: row => row.id,
  })
  const artifacts = usePagedList({
    params: { taskId, sort_by: 'created_at', sort_dir: 'desc' as const, include_total: true },
    fetchPage: ({ params, cursor, signal }) => api.listArtifacts(params.taskId, { sort_by: params.sort_by, sort_dir: params.sort_dir, include_total: params.include_total, cursor }, signal),
    getId: row => row.id,
  })
  return <>
    <p className="mb-2 text-xs text-muted-foreground">Timeline merges the loaded run, comment, and artifact pages with live activity. Older pages may contain events between the rows shown.</p>
    <ListPageControls page={runs} label="runs" />
    <ListPageControls page={comments} label="comments" />
    <ListPageControls page={artifacts} label="artifacts" />
    <ActivityTimeline taskId={taskId} runs={runs.items} comments={comments.items} artifacts={artifacts.items} />
  </>
}
