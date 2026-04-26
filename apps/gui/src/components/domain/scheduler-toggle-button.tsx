import { useEffect, useRef, useState } from 'react'
import { Pause, Play } from 'lucide-react'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import { cn } from '@/lib/utils'
import type { SchedulerStatus } from '@/lib/types'

const POLL_INTERVAL_MS = 5000

/**
 * Global scheduler enable/disable toggle. Polls /scheduler/status on mount and
 * at a slow cadence so external HTTP toggles (other tabs, curl, etc.) stay in
 * sync without needing SSE plumbing.
 */
export function SchedulerToggleButton() {
  const api = useApi()
  const [status, setStatus] = useState<SchedulerStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(false)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    let cancelled = false

    async function load() {
      try {
        const s = await api.getSchedulerStatus()
        if (!cancelled && mounted.current) {
          setStatus(s)
          setError(false)
        }
      } catch {
        if (!cancelled && mounted.current) setError(true)
      }
    }

    load()
    const id = window.setInterval(load, POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      mounted.current = false
      window.clearInterval(id)
    }
  }, [api])

  async function handleToggle() {
    if (busy) return
    setBusy(true)
    try {
      const next = await api.toggleScheduler()
      setStatus(next)
      setError(false)
      notifySuccess(next.enabled ? 'Scheduler started' : 'Scheduler paused')
    } catch (err) {
      notifyError(err, 'Failed to toggle scheduler')
    } finally {
      setBusy(false)
    }
  }

  if (error) {
    return (
      <button
        type="button"
        disabled
        title="Scheduler endpoint unreachable"
        className="inline-flex h-7 items-center gap-1.5 rounded border border-zinc-800 bg-zinc-900/50 px-2 text-[10px] uppercase tracking-wider text-zinc-600"
      >
        <Pause className="h-3 w-3" />
        Scheduler ?
      </button>
    )
  }

  const enabled = status?.enabled ?? false
  const label = enabled ? 'Running' : 'Paused'
  const Icon = enabled ? Pause : Play
  const action = enabled ? 'Pause scheduler' : 'Start scheduler'

  return (
    <button
      type="button"
      onClick={handleToggle}
      disabled={busy || !status}
      aria-label={action}
      title={action}
      className={cn(
        'inline-flex h-7 items-center gap-1.5 rounded border px-2 text-[10px] uppercase tracking-wider transition-colors',
        enabled
          ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-200 hover:border-emerald-500/60'
          : 'border-amber-500/40 bg-amber-500/10 text-amber-200 hover:border-amber-500/60',
        busy && 'opacity-50',
      )}
    >
      <Icon className="h-3 w-3" aria-hidden />
      <span>{busy ? '…' : label}</span>
      {status && (
        <span className="font-mono tabular-nums text-zinc-500">
          {status.active_workers}/{status.max_workers}
        </span>
      )}
    </button>
  )
}
