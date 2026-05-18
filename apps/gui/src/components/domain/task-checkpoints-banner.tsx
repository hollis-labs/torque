import { useEffect, useState, useCallback } from 'react'
import { AlertCircle } from 'lucide-react'
import { Button } from '@hollis-labs/sysop-ui'
import { CheckpointRespondDialog } from './checkpoint-respond-dialog'
import { CheckpointCancelDialog } from './checkpoint-cancel-dialog'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import type { Checkpoint } from '@/lib/types'

const SSE_EVENTS = ['checkpoint.emitted', 'checkpoint.responded', 'checkpoint.canceled']

interface TaskCheckpointsBannerProps {
  taskId: string
}

export function TaskCheckpointsBanner({ taskId }: TaskCheckpointsBannerProps) {
  const api = useApi()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [pending, setPending] = useState<Checkpoint[]>([])
  const [respondTarget, setRespondTarget] = useState<Checkpoint | null>(null)
  const [cancelTarget, setCancelTarget] = useState<Checkpoint | null>(null)

  const fetchCheckpoints = useCallback(async () => {
    try {
      const { checkpoints } = await api.listTaskCheckpoints(taskId)
      setPending(checkpoints.filter((c) => c.status === 'pending'))
    } catch {
      setPending([])
    }
  }, [api, taskId])

  useEffect(() => {
    fetchCheckpoints()
  }, [fetchCheckpoints])

  useEffect(() => {
    if (lastEvent) fetchCheckpoints()
  }, [lastEvent, fetchCheckpoints])

  if (pending.length === 0) return null

  return (
    <>
      <div className="border-b border-amber-500/30 bg-amber-500/10 px-4 py-2.5">
        <div className="flex items-center gap-2 mb-2">
          <AlertCircle className="h-4 w-4 text-amber-300" />
          <span className="text-xs font-medium text-amber-200">
            {pending.length} pending checkpoint{pending.length === 1 ? '' : 's'}
          </span>
        </div>
        <ul className="flex flex-col gap-1.5">
          {pending.map((cp) => (
            <li
              key={cp.correlation_id}
              className="flex items-center justify-between gap-3 rounded border border-amber-500/20 bg-zinc-950/50 px-2.5 py-1.5"
            >
              <div className="flex items-center gap-2 min-w-0">
                <span className="inline-block rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-amber-200">
                  {cp.type}
                </span>
                <span className="font-mono text-[10px] text-zinc-500 truncate">
                  {cp.correlation_id}
                </span>
              </div>
              <div className="flex items-center gap-1.5">
                <Button size="sm" variant="outline" onClick={() => setCancelTarget(cp)}>
                  Cancel
                </Button>
                <Button size="sm" onClick={() => setRespondTarget(cp)}>
                  Respond
                </Button>
              </div>
            </li>
          ))}
        </ul>
      </div>

      <CheckpointRespondDialog
        checkpoint={respondTarget}
        open={respondTarget !== null}
        onOpenChange={(open) => {
          if (!open) setRespondTarget(null)
        }}
        onResponded={fetchCheckpoints}
      />
      <CheckpointCancelDialog
        checkpoint={cancelTarget}
        open={cancelTarget !== null}
        onOpenChange={(open) => {
          if (!open) setCancelTarget(null)
        }}
        onCanceled={fetchCheckpoints}
      />
    </>
  )
}
