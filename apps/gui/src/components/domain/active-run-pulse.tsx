import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useActiveRun } from '@/hooks/active-runs-context'
import { useElapsed } from '@/hooks/use-elapsed'

interface ActiveRunPulseProps {
  taskId: string
}

function formatElapsed(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  const m = Math.floor(s / 60)
  const rem = s % 60
  return `${m}m ${rem.toString().padStart(2, '0')}s`
}

function truncate(text: string, max = 80): string {
  if (text.length <= max) return text
  return `${text.slice(0, max - 1)}…`
}

// ActiveRunPulse renders a small pulsing amber dot next to the status pill
// when the given task has a live run. The tooltip carries elapsed time and
// the most recent note text so overview scanners can tell which runs are
// actually making progress without opening the detail page. Hidden whenever
// the task has no active run, so it's safe to render unconditionally inline.
export function ActiveRunPulse({ taskId }: ActiveRunPulseProps) {
  const activeRun = useActiveRun(taskId)
  const elapsed = useElapsed(activeRun?.startedAt ?? null)

  if (!activeRun) return null

  const noteLine = activeRun.lastNote
    ? `last note: ${truncate(activeRun.lastNote)}`
    : 'waiting for first note'

  return (
    <Tooltip>
      <TooltipTrigger
        render={(props) => (
          <span
            {...props}
            tabIndex={0}
            aria-label={`Run in progress, elapsed ${formatElapsed(elapsed)}`}
            className="relative inline-flex h-2 w-2 items-center justify-center"
          >
            <span className="absolute inset-0 animate-ping rounded-full bg-amber-400/80" />
            <span className="relative inline-block h-1.5 w-1.5 rounded-full bg-amber-400" />
          </span>
        )}
      />
      <TooltipContent className="max-w-sm whitespace-pre-wrap text-left">
        <div className="text-[11px] uppercase tracking-[.14em] text-zinc-300">
          elapsed {formatElapsed(elapsed)}
        </div>
        <div className="mt-0.5 text-[11px] text-zinc-400">{noteLine}</div>
      </TooltipContent>
    </Tooltip>
  )
}
