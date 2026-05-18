import type { ReactNode } from 'react'
import { StatusBadge } from '@/components/domain/status-badge'
import { ProgressBar } from '@hollis-labs/sysop-ui'

interface HeroMetric {
  label: string
  value: string | number
  accentColor?: string
}

interface HeroMeta {
  label: string
  value: ReactNode
}

interface ScopeDetailHeroProps {
  kindLabel: string
  title: string
  id: string
  status: string
  description?: string
  progress: {
    total: number
    done: number
    completion: number
  }
  metrics: HeroMetric[]
  meta?: HeroMeta[]
  actions?: ReactNode
}

export function ScopeDetailHero({
  kindLabel,
  title,
  id,
  status,
  description,
  progress,
  metrics,
  meta = [],
  actions,
}: ScopeDetailHeroProps) {
  return (
    <section className="rounded-2xl border border-emerald-950/70 bg-zinc-950/80 p-5 shadow-[inset_0_1px_0_rgba(255,255,255,0.02)]">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="mb-4 flex flex-wrap items-center gap-2 text-[11px] uppercase tracking-[0.22em] text-zinc-500">
            <span className="rounded-md border border-zinc-800 bg-zinc-900 px-2.5 py-1 font-semibold text-zinc-300">
              {kindLabel}
            </span>
            <StatusBadge status={status} />
            <span className="font-mono text-zinc-600">{id}</span>
          </div>
          <h1 className="text-3xl font-semibold tracking-[0.01em] text-zinc-50">{title}</h1>
          {description && (
            <p className="mt-4 max-w-5xl whitespace-pre-wrap text-base leading-8 text-zinc-400">
              {description}
            </p>
          )}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>

      <div className="mt-10">
        <div className="mb-3 flex items-center justify-between gap-3 text-[11px] uppercase tracking-[0.22em] text-zinc-500">
          <span>Progress</span>
          <span className="font-mono text-zinc-400">
            {progress.done}/{progress.total} ({progress.completion}%)
          </span>
        </div>
        <ProgressBar value={progress.completion} className="h-4 rounded-full bg-zinc-900" />
      </div>

      <div className="mt-5 flex flex-wrap gap-x-5 gap-y-2 border-b border-zinc-900/90 pb-5">
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
        <div className="mt-5 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
          {meta.map((item) => (
            <div key={item.label} className="rounded-xl border border-zinc-900 bg-zinc-950/70 p-4">
              <div className="mb-2 text-[11px] uppercase tracking-[0.18em] text-zinc-500">{item.label}</div>
              <div className="text-sm text-zinc-200">{item.value}</div>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
