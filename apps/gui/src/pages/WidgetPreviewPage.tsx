import { useEffect, useState } from 'react'
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
import type { Run, Task } from '@/lib/types'

export default function WidgetPreviewPage() {
  const api = useApi()
  const [tasks, setTasks] = useState<Task[]>([])
  const [runs, setRuns] = useState<Run[]>([])

  useEffect(() => {
    let cancelled = false
    api
      .listTasks({ limit: 500 })
      .then(({ tasks: t }) => {
        if (cancelled) return
        setTasks(t)
        const recent = t
          .slice()
          .sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
          .slice(0, 20)
        return Promise.all(recent.map((x) => api.listRuns(x.id).catch(() => [] as Run[])))
      })
      .then((lists) => {
        if (cancelled || !lists) return
        setRuns(lists.flat())
      })
      .catch(() => {
        // swallow; widgets render empty states
      })
    return () => {
      cancelled = true
    }
  }, [api])

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Widget Preview" />
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
