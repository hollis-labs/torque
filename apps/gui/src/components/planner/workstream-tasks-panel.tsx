import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { PlannerStatus } from '@/components/planner/planner-status'
import { useApi } from '@/hooks/use-api'
import type { PlannerWorkstreamTasks } from '@/lib/types'

const ORDER = ['doing', 'review', 'queued', 'blocked', 'paused', 'todo', 'backlog', 'done', 'archived', 'abandoned', 'cancelled']

/** The Torque tasks behind a workstream: status counts and the open ones. */
export function WorkstreamTasksPanel({ id, refreshKey }: { id: string; refreshKey?: unknown }) {
  const api = useApi()
  const [data, setData] = useState<PlannerWorkstreamTasks | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const ctl = new AbortController()
    api.plannerWorkstreamTasks(id, ctl.signal).then((d) => { setData(d); setFailed(false) }).catch(() => { if (!ctl.signal.aborted) setFailed(true) })
    return () => ctl.abort()
  }, [api, id, refreshKey])

  if (failed) return null
  if (!data) return null
  const scope = [...data.torque_project_ids, data.torque_tag ? `tag ${data.torque_tag}` : ''].filter(Boolean).join(' · ')
  const counts = ORDER.filter((s) => data.counts[s]).map((s) => ({ status: s, n: data.counts[s] }))

  return (
    <section aria-label="Torque tasks" className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between gap-2">
        <h3 className="text-sm font-semibold">Torque tasks{data.total ? ` (${data.total})` : ''}</h3>
        {scope && <span className="truncate text-xs text-muted-foreground">{scope}</span>}
      </div>
      {data.note ? (
        <p className="text-sm text-muted-foreground">{data.note} Set torque_project_ids or torque_tag on it to see its tasks here.</p>
      ) : data.total === 0 ? (
        <p className="text-sm text-muted-foreground">No Torque tasks match this workstream&apos;s projects and tag.</p>
      ) : (
        <>
          <div className="flex flex-wrap gap-1.5">
            {counts.map((c) => (
              <span key={c.status} className="inline-flex items-center gap-1.5">
                <PlannerStatus status={c.status} className="!px-1.5 !py-0.5 !text-[10px]" />
                <span className="text-xs text-muted-foreground">{c.n}</span>
              </span>
            ))}
          </div>
          {data.open.length > 0 && (
            <ul className="flex flex-col gap-1">
              {data.open.map((t) => (
                <li key={t.id} className="flex items-center gap-2 text-sm">
                  <Link to={`/tasks/${encodeURIComponent(t.id)}`} className="shrink-0 font-mono text-xs text-primary hover:underline">
                    {t.id}
                  </Link>
                  <span className="min-w-0 flex-1 truncate">{t.title}</span>
                  <PlannerStatus status={t.status} className="!px-1.5 !py-0.5 !text-[10px]" />
                </li>
              ))}
            </ul>
          )}
          {data.open_total > data.open.length && <p className="text-xs text-muted-foreground">Showing {data.open.length} of {data.open_total} open tasks.</p>}
        </>
      )}
    </section>
  )
}
