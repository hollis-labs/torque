import { useState } from 'react'
import { Check, CircleAlert } from 'lucide-react'
import { useApi } from '@/hooks/use-api'
import { notifyError } from '@/lib/toast'
import { cn } from '@/lib/utils'
import type { Subtodo } from '@/lib/types'

interface SubtodosPanelProps {
  taskId: string
  subtodos: Subtodo[]
  onChange: (next: Subtodo[]) => void
}

export function SubtodosPanel({ taskId, subtodos, onChange }: SubtodosPanelProps) {
  const api = useApi()
  const [pending, setPending] = useState<string | null>(null)

  const total = subtodos.length
  const done = subtodos.filter((s) => s.done).length
  const allDone = total > 0 && done === total
  const requiredOpen = subtodos.some((s) => s.required && !s.done)

  async function handleMark(item: Subtodo) {
    if (item.done || pending) return
    setPending(item.id)
    try {
      const updated = await api.markSubtodoDone(taskId, item.id)
      onChange(updated)
    } catch (err) {
      notifyError(err, 'Failed to mark subtodo done')
    } finally {
      setPending(null)
    }
  }

  if (total === 0) {
    return (
      <div className="rounded-md border border-zinc-800/60 bg-zinc-950 px-4 py-6">
        <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">Subtodos</span>
        <p className="mt-1 text-[13px] text-zinc-500">
          No checklist on this task. Add `- [ ]` items to the description to seed one, or use the
          MCP <code className="font-mono text-zinc-400">clockwork_task_subtodo_add</code> tool.
        </p>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-3">
        <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">Subtodos</span>
        <span
          className={cn(
            'font-mono text-[11px] tabular-nums',
            allDone ? 'text-emerald-400' : 'text-zinc-300',
          )}
        >
          {done}/{total}
        </span>
        {requiredOpen && (
          <span
            className="inline-flex items-center gap-1 text-[10px] uppercase tracking-[.14em] text-amber-400"
            title="Required items remain — task is gated from review until they're checked off."
          >
            <CircleAlert className="h-3 w-3" aria-hidden />
            Gate open
          </span>
        )}
      </div>

      <ul className="flex flex-col divide-y divide-zinc-800/60 rounded-md border border-zinc-800/60 bg-zinc-950">
        {subtodos.map((item) => {
          const busy = pending === item.id
          return (
            <li key={item.id} className="flex items-start gap-3 px-3 py-2">
              <button
                type="button"
                onClick={() => handleMark(item)}
                disabled={item.done || busy}
                aria-checked={item.done}
                role="checkbox"
                aria-label={`Mark ${item.text} done`}
                className={cn(
                  'mt-0.5 flex h-4 w-4 flex-none items-center justify-center rounded border transition-colors',
                  item.done
                    ? 'border-emerald-600 bg-emerald-600 text-zinc-950'
                    : 'border-zinc-700 bg-zinc-900 hover:border-zinc-500',
                  busy && 'opacity-50',
                )}
              >
                {item.done && <Check className="h-3 w-3" aria-hidden />}
              </button>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span
                    className={cn(
                      'text-[13px]',
                      item.done ? 'text-zinc-500 line-through' : 'text-zinc-200',
                    )}
                  >
                    {item.text}
                  </span>
                  {item.required && !item.done && (
                    <span className="text-[9px] uppercase tracking-[.18em] text-amber-400/80">
                      required
                    </span>
                  )}
                </div>
                {item.evidence && (
                  <div className="mt-0.5 font-mono text-[11px] text-zinc-500 break-all">
                    {item.evidence}
                  </div>
                )}
              </div>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

/**
 * Compact `done/total` chip for the TasksTable. Green when every item is
 * checked, zinc otherwise. Returns null when the task has no checklist so
 * the column stays clean for the many tasks that never use subtodos.
 */
export function SubtodosBadge({ subtodos }: { subtodos: Subtodo[] | undefined }) {
  if (!subtodos || subtodos.length === 0) return null
  const done = subtodos.filter((s) => s.done).length
  const total = subtodos.length
  const allDone = done === total
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-sm border px-1 py-0 font-mono text-[10px] leading-4 tabular-nums',
        allDone
          ? 'border-emerald-700/50 bg-emerald-900/30 text-emerald-300'
          : 'border-zinc-700/70 bg-zinc-900 text-zinc-300',
      )}
      title={`${done} of ${total} checklist items done`}
    >
      {done}/{total}
    </span>
  )
}
