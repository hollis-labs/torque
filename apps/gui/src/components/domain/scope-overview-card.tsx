import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { StatusBadge } from '@/components/domain/status-badge'
import { ProgressBar } from '@hollis-labs/sysop-ui'

interface ScopeOverviewCardProps {
  kindLabel: string
  title: string
  to: string
  id: string
  status: string
  description?: string
  progress: {
    total: number
    done: number
    completion: number
  }
  metrics: Array<{
    label: string
    value: string | number
    accentColor?: string
  }>
  meta?: Array<{
    label: string
    value: ReactNode
  }>
  actions?: ReactNode
}

export function ScopeOverviewCard({
  kindLabel,
  title,
  to,
  id,
  status,
  description,
  progress,
  metrics,
  meta = [],
  actions,
}: ScopeOverviewCardProps) {
  return (
    <section className="rounded-2xl border border-zinc-800/80 bg-zinc-950/75 p-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="mb-3 flex flex-wrap items-center gap-2 text-[11px] uppercase tracking-[0.2em] text-zinc-500">
            <span className="rounded-md border border-zinc-800 bg-zinc-900 px-2.5 py-1 font-semibold text-zinc-300">
              {kindLabel}
            </span>
            <StatusBadge status={status} />
            <span className="font-mono text-zinc-600">{id}</span>
          </div>
          <Link to={to} className="block text-2xl font-semibold tracking-[0.01em] text-zinc-50 hover:text-zinc-200">
            {title}
          </Link>
          {description && (
            <p className="mt-3 line-clamp-3 whitespace-pre-wrap text-sm leading-7 text-zinc-400">
              {description}
            </p>
          )}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>

      <div className="mt-6">
        <div className="mb-2 flex items-center justify-between gap-3 text-[11px] uppercase tracking-[0.2em] text-zinc-500">
          <span>Task Progress</span>
          <span className="font-mono text-zinc-400">
            {progress.done}/{progress.total} ({progress.completion}%)
          </span>
        </div>
        <ProgressBar value={progress.completion} className="h-3 rounded-full bg-zinc-900" />
      </div>

      <div className="mt-4 flex flex-wrap gap-x-5 gap-y-2">
        {metrics.map((metric, index) => (
          <div key={metric.label} className="flex items-center gap-2 text-[11px] leading-none">
            <span
              className="size-1.5 shrink-0 rounded-full"
              style={{ backgroundColor: metric.accentColor ?? '#71717a' }}
              aria-hidden
            />
            <span className="uppercase tracking-[0.16em] text-zinc-500">{metric.label}</span>
            <span className="font-mono text-[13px] font-semibold text-zinc-100">{metric.value}</span>
            {index < metrics.length - 1 && <span className="ml-3 h-3 w-px bg-zinc-800/80" aria-hidden />}
          </div>
        ))}
      </div>

      {meta.length > 0 && (
        <div className="mt-5 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          {meta.map((item) => (
            <div key={item.label} className="rounded-xl border border-zinc-900 bg-zinc-950/70 p-3">
              <div className="mb-1 text-[11px] uppercase tracking-[0.18em] text-zinc-500">{item.label}</div>
              <div className="text-sm text-zinc-200">{item.value}</div>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
