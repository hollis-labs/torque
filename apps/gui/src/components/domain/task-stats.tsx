import { Coins, Cpu, Repeat } from 'lucide-react'
import { formatCost, formatTokens } from '@/lib/utils'
import type { TaskStats as TaskStatsData } from '@/lib/types'

interface TaskStatsProps {
  stats: TaskStatsData | undefined
}

/**
 * Tooltip text for the cost source. Centralized so the strip and compact
 * variants stay in sync.
 */
function costSourceTitle(source: TaskStatsData['cost_source'], runCount: number): string {
  const base = `Cost across ${runCount} run${runCount === 1 ? '' : 's'}`
  switch (source) {
    case 'measured':
      return `${base} (measured: executor reported cost_usd)`
    case 'estimated':
      return `${base} (estimated via models.dev pricing — exact cost not reported, common under subscription billing)`
    case 'unknown':
    case '':
    default:
      return `${base} (no cost data — ledger row missing or pre-migration-017)`
  }
}

/**
 * Detail-header stat strip: turn count · total tokens · total cost.
 * Hidden entirely when no runs have executed so the header stays calm
 * for brand-new tasks. The cost figure adapts to `cost_source`:
 * `~$x.xx` + amber badge for estimated, plain `$x.xx` for measured,
 * `—` for unknown.
 */
export function TaskStatsStrip({ stats }: TaskStatsProps) {
  if (!stats || stats.run_count === 0) return null
  const total = stats.prompt_tokens + stats.completion_tokens
  const turnLabel = stats.run_count === 1 ? '1 turn' : `${stats.run_count} turns`
  const isEstimated = stats.cost_source === 'estimated'
  return (
    <div className="flex items-center gap-4 text-[11px] text-zinc-400 tabular-nums">
      <span
        className="inline-flex items-center gap-1"
        title={`${stats.run_count} run${stats.run_count === 1 ? '' : 's'}`}
      >
        <Repeat className="h-3 w-3 text-zinc-500" aria-hidden />
        <span className="uppercase tracking-[.12em] text-zinc-500">{turnLabel}</span>
      </span>
      <span
        className="inline-flex items-center gap-1"
        title={`prompt ${stats.prompt_tokens.toLocaleString()} · completion ${stats.completion_tokens.toLocaleString()}`}
      >
        <Cpu className="h-3 w-3 text-zinc-500" aria-hidden />
        <span>{formatTokens(total)}</span>
      </span>
      <span
        className="inline-flex items-center gap-1"
        title={costSourceTitle(stats.cost_source, stats.run_count)}
      >
        <Coins className="h-3 w-3 text-amber-500/70" aria-hidden />
        <span className="text-zinc-300">{formatCost(stats.cost, stats.cost_source)}</span>
        {isEstimated && (
          <span
            className="rounded border border-amber-700/40 bg-amber-950/40 px-1 py-px text-[9px] uppercase tracking-[.10em] text-amber-400"
            aria-label="Cost is an estimate"
          >
            est
          </span>
        )}
      </span>
    </div>
  )
}

/**
 * Compact `$0.00 · 2K` cell for TaskTable rows. Returns an em dash when
 * the task has never run so the column stays aligned without visual noise.
 * The cost figure carries a `~` prefix when the source is `estimated`,
 * and renders as `—` when source is `unknown`/empty (no ledger data).
 * The badge is omitted in this dense variant — the `~` prefix carries
 * the signal so the cell stays single-line at narrow column widths.
 */
export function TaskStatsCompact({ stats }: TaskStatsProps) {
  if (!stats || stats.run_count === 0) {
    return <span className="text-[11px] text-zinc-700">—</span>
  }
  const total = stats.prompt_tokens + stats.completion_tokens
  return (
    <span
      className="inline-flex items-center gap-1 text-[11px] tabular-nums text-zinc-400"
      title={`${stats.run_count} run${stats.run_count === 1 ? '' : 's'} · prompt ${stats.prompt_tokens.toLocaleString()} · completion ${stats.completion_tokens.toLocaleString()} · ${costSourceTitle(stats.cost_source, stats.run_count)}`}
    >
      <span className="text-zinc-300">{formatCost(stats.cost, stats.cost_source)}</span>
      <span className="text-zinc-600">·</span>
      <span>{formatTokens(total)}</span>
    </span>
  )
}
