import { Coins, Cpu, Repeat } from 'lucide-react'
import { formatCost, formatTokens } from '@/lib/utils'
import type { TaskStats as TaskStatsData } from '@/lib/types'

interface TaskStatsProps {
  stats: TaskStatsData | undefined
}

/**
 * Detail-header stat strip: turn count · total tokens · total cost.
 * Hidden entirely when no runs have executed so the header stays calm
 * for brand-new tasks.
 */
export function TaskStatsStrip({ stats }: TaskStatsProps) {
  if (!stats || stats.run_count === 0) return null
  const total = stats.prompt_tokens + stats.completion_tokens
  const turnLabel = stats.run_count === 1 ? '1 turn' : `${stats.run_count} turns`
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
      <span className="inline-flex items-center gap-1" title={`Cost across ${stats.run_count} run(s)`}>
        <Coins className="h-3 w-3 text-amber-500/70" aria-hidden />
        <span className="text-zinc-300">{formatCost(stats.cost)}</span>
      </span>
    </div>
  )
}

/**
 * Compact `$0.00 · 2K` cell for TaskTable rows. Returns an em dash when
 * the task has never run so the column stays aligned without visual noise.
 */
export function TaskStatsCompact({ stats }: TaskStatsProps) {
  if (!stats || stats.run_count === 0) {
    return <span className="text-[11px] text-zinc-700">—</span>
  }
  const total = stats.prompt_tokens + stats.completion_tokens
  return (
    <span
      className="inline-flex items-center gap-1 text-[11px] tabular-nums text-zinc-400"
      title={`${stats.run_count} run${stats.run_count === 1 ? '' : 's'} · prompt ${stats.prompt_tokens.toLocaleString()} · completion ${stats.completion_tokens.toLocaleString()}`}
    >
      <span className="text-zinc-300">{formatCost(stats.cost)}</span>
      <span className="text-zinc-600">·</span>
      <span>{formatTokens(total)}</span>
    </span>
  )
}
