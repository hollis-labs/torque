import { LiveDot, Tooltip, TooltipContent, TooltipTrigger, useElapsed } from '@hollis-labs/sysop-ui'
import { useActiveRun } from '@/hooks/active-runs-context'

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

// ActiveRunPulse renders a small pulsing dot next to the status pill when the
// given task has a live run. The tooltip carries elapsed time and the most
// recent note text so overview scanners can tell which runs are actually
// making progress without opening the detail page. Hidden whenever the task
// has no active run, so it's safe to render unconditionally inline. The dot +
// elapsed tick are the kit's `LiveDot` / `useElapsed`; the run lookup and
// tooltip copy stay domain-local.
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
          <span {...props} tabIndex={0} className="inline-flex">
            <LiveDot
              tone="warning"
              label={`Run in progress, elapsed ${formatElapsed(elapsed)}`}
            />
          </span>
        )}
      />
      <TooltipContent className="max-w-sm whitespace-pre-wrap text-left">
        <div className="text-[11px] uppercase tracking-[.14em] text-text-muted">
          elapsed {formatElapsed(elapsed)}
        </div>
        <div className="mt-0.5 text-[11px] text-text-soft">{noteLine}</div>
      </TooltipContent>
    </Tooltip>
  )
}
