import { PageHeader } from '@hollis-labs/sysop-ui'
import {
  ActivityHeatmap,
  RunsChart,
  TaskPipeline,
  TokenThroughput,
  RunStatusDistribution,
  RecentRuns,
  Pulse24h,
  CostPerDay,
} from '@/components/widgets'
import { useApi } from '@/hooks/use-api'
import { usePagedList } from '@/hooks/use-paged-list'
import { ListPageControls } from '@/components/domain/list-page-controls'

export default function WidgetPreviewPage() {
  const api = useApi()
  const taskPage = usePagedList({
    params: { limit: 50, sort_by: 'updated_at' as const, sort_dir: 'desc' as const, include_total: true },
    fetchPage: ({ params, cursor, signal }) => api.listTaskPage({ ...params, cursor }, signal),
    getId: task => task.id,
  })
  const runPage = usePagedList({
    params: { limit: 50, sort_by: 'started_at' as const, sort_dir: 'desc' as const, include_total: true },
    fetchPage: ({ params, cursor, signal }) => api.pageRuns({ ...params, cursor }, signal),
    getId: run => run.id,
  })
  const tasks = taskPage.items
  const runs = runPage.items

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Widget Preview" />
      <p className="px-4 py-2 text-sm text-muted-foreground">Preview charts summarize the loaded task and run pages only. They are not whole-project aggregates.</p>
      <ListPageControls page={taskPage} label="tasks" />
      <ListPageControls page={runPage} label="runs" />
      <div className="flex flex-1 flex-col gap-6 overflow-auto p-4">
        <section className="rounded border border-border/60 bg-card/40 p-4">
          <h2 className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-muted-foreground">
            Activity Heatmap
          </h2>
          <ActivityHeatmap tasks={tasks} runs={runs} />
        </section>

        <section className="rounded border border-border/60 bg-card/40 p-4">
          <h2 className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-muted-foreground">
            Runs Chart
          </h2>
          <RunsChart runs={runs} />
        </section>

        <section className="max-w-sm rounded border border-border/60 bg-card/40 p-4">
          <h2 className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-muted-foreground">
            Task Pipeline
          </h2>
          <TaskPipeline tasks={tasks} />
        </section>

        <section className="rounded border border-border/60 bg-card/40 p-4">
          <h2 className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-muted-foreground">
            Token Throughput
          </h2>
          <TokenThroughput runs={runs} />
        </section>

        <section className="max-w-md rounded border border-border/60 bg-card/40 p-4">
          <h2 className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-muted-foreground">
            Run Status Distribution
          </h2>
          <RunStatusDistribution runs={runs} />
        </section>

        <section className="max-w-md rounded border border-border/60 bg-card/40 p-4">
          <h2 className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-muted-foreground">
            Recent Runs
          </h2>
          <RecentRuns runs={runs} />
        </section>

        <section className="rounded border border-border/60 bg-card/40 p-4">
          <h2 className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-muted-foreground">
            24h Pulse
          </h2>
          <Pulse24h runs={runs} />
        </section>

        <section className="rounded border border-border/60 bg-card/40 p-4">
          <h2 className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-muted-foreground">
            Cost Per Day
          </h2>
          <CostPerDay runs={runs} />
        </section>
      </div>
    </div>
  )
}
