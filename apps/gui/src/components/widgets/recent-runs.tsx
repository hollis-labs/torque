import { useMemo } from 'react'
import { RecentList } from '@hollis-labs/sysop-ui/widgets'
import type { Run } from '@/lib/types'

export interface RecentRunsProps {
  runs: Run[]
  limit?: number
  className?: string
  title?: string
  onSelect?: (run: Run) => void
}

function classifyDotColor(status: string): string {
  const s = (status || '').toLowerCase()
  if (s === 'success' || s === 'done' || s === 'completed') return 'var(--color-status-done)'
  if (s === 'error' || s === 'failed' || s === 'cancelled' || s === 'canceled' || s === 'timeout')
    return 'var(--color-status-blocked)'
  if (s === 'running' || s === 'doing' || s === 'started' || s === 'pending' || s === 'queued')
    return 'var(--color-status-doing)'
  return 'var(--color-status-backlog)'
}

function relativeTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  const then = new Date(iso).getTime()
  if (!Number.isFinite(then)) return '—'
  const diff = Date.now() - then
  if (diff < 0) return 'now'
  const sec = Math.round(diff / 1000)
  if (sec < 60) return `${sec}s`
  const min = Math.round(sec / 60)
  if (min < 60) return `${min}m`
  const hr = Math.round(min / 60)
  if (hr < 24) return `${hr}h`
  const day = Math.round(hr / 24)
  if (day < 30) return `${day}d`
  const mo = Math.round(day / 30)
  return `${mo}mo`
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return `${n}`
}

function shortId(s: string): string {
  return s.length > 12 ? `${s.slice(0, 4)}…${s.slice(-4)}` : s
}

/**
 * Recent-runs list — a thin wrapper over the kit's RecentList. Torque sorts
 * newest-first and supplies the run-row renderer; the kit owns the shell.
 */
export function RecentRuns({
  runs,
  limit = 12,
  className,
  title = 'Recent runs',
  onSelect,
}: RecentRunsProps) {
  const sorted = useMemo(
    () =>
      runs.slice().sort((a, b) => {
        const ta = a.started_at ? new Date(a.started_at).getTime() : 0
        const tb = b.started_at ? new Date(b.started_at).getTime() : 0
        return tb - ta
      }),
    [runs],
  )

  return (
    <RecentList<Run>
      items={sorted}
      getKey={(r) => r.id}
      limit={limit}
      title={title}
      onSelect={onSelect}
      emptyLabel="No runs yet"
      className={className}
      renderItem={(r) => {
        const tokens = (r.prompt_tokens ?? 0) + (r.completion_tokens ?? 0)
        return (
          <div className="flex items-center gap-2 px-1 py-1.5 text-[11px]">
            <span
              className="inline-block h-2 w-2 shrink-0 rounded-sm"
              style={{ backgroundColor: classifyDotColor(r.status) }}
              aria-hidden
            />
            <span className="font-mono tabular-nums text-muted-foreground/80">#{r.id}</span>
            <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground/90">
              {shortId(r.task_id)}
            </span>
            <span className="hidden font-mono text-[10px] text-muted-foreground/60 sm:inline">
              {r.agent_profile || r.executor || '—'}
            </span>
            <span className="font-mono tabular-nums text-muted-foreground">
              {formatTokens(tokens)}
            </span>
            <span className="w-10 text-right font-mono tabular-nums text-muted-foreground/70">
              {relativeTime(r.started_at)}
            </span>
          </div>
        )
      }}
    />
  )
}
