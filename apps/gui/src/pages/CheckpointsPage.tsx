import { useState, useEffect, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { Skeleton, Button, PageHeader, SummaryCards, CopyableId, EmptyState } from '@hollis-labs/sysop-ui'
import { CheckpointRespondDialog } from '@/components/domain/checkpoint-respond-dialog'
import { CheckpointCancelDialog } from '@/components/domain/checkpoint-cancel-dialog'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import type { Checkpoint } from '@/lib/types'

const SSE_EVENTS = ['checkpoint.emitted', 'checkpoint.responded', 'checkpoint.canceled']

function TableSkeleton() {
  return (
    <div className="flex flex-col gap-2 p-4">
      {Array.from({ length: 4 }).map((_, i) => (
        <Skeleton key={i} className="h-16 w-full rounded-md" />
      ))}
    </div>
  )
}

function formatPayload(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

function elapsed(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

export default function CheckpointsPage() {
  const api = useApi()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [checkpoints, setCheckpoints] = useState<Checkpoint[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<Checkpoint | null>(null)
  const [respondTarget, setRespondTarget] = useState<Checkpoint | null>(null)
  const [cancelTarget, setCancelTarget] = useState<Checkpoint | null>(null)

  const fetchPending = useCallback(async () => {
    try {
      const result = await api.listPendingCheckpoints()
      setCheckpoints(result.checkpoints ?? [])
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load checkpoints')
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    setLoading(true)
    fetchPending()
  }, [fetchPending])

  useEffect(() => {
    if (lastEvent) fetchPending()
  }, [lastEvent, fetchPending])

  const summaryCards = [
    { label: 'Pending', value: checkpoints.length, accentColor: '#f59e0b' },
    {
      label: 'Tasks',
      value: new Set(checkpoints.map((c) => c.task_id)).size,
      subtitle: 'with pending',
    },
    {
      label: 'Oldest',
      value: checkpoints.length
        ? elapsed(checkpoints.reduce((a, b) => (a.emitted_at < b.emitted_at ? a : b)).emitted_at)
        : '\u2014',
    },
    {
      label: 'Types',
      value: new Set(checkpoints.map((c) => c.type)).size,
    },
  ]

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Checkpoints · Pending" />

      {!loading && !error && <SummaryCards cards={summaryCards} />}

      <div className="flex-1 overflow-hidden">
        <div className="grid h-full grid-cols-[minmax(320px,1fr)_minmax(400px,1.3fr)]">
          <div className="flex flex-col overflow-auto border-r border-zinc-800/80">
            {loading ? (
              <TableSkeleton />
            ) : error ? (
              <EmptyState
                variant="error"
                title="Something went wrong"
                description={error}
                action={{ label: 'Retry', onClick: fetchPending }}
              />
            ) : checkpoints.length === 0 ? (
              <EmptyState
                variant="empty"
                title="Inbox is empty"
                description="No pending checkpoints waiting for a response."
              />
            ) : (
              <ul className="divide-y divide-zinc-800/60">
                {checkpoints.map((cp) => {
                  const active = selected?.correlation_id === cp.correlation_id
                  return (
                    <li key={cp.correlation_id}>
                      <button
                        type="button"
                        onClick={() => setSelected(cp)}
                        className={`flex w-full flex-col gap-1 px-4 py-3 text-left transition-colors ${
                          active ? 'bg-zinc-900' : 'hover:bg-zinc-900/50'
                        }`}
                      >
                        <div className="flex items-center justify-between gap-2">
                          <span className="inline-block rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-amber-200">
                            {cp.type}
                          </span>
                          <span className="text-[10px] text-zinc-500 whitespace-nowrap">
                            {elapsed(cp.emitted_at)}
                          </span>
                        </div>
                        <div className="flex items-center gap-2 text-xs text-zinc-300">
                          <span className="text-zinc-500">task</span>
                          <span className="font-mono truncate">{cp.task_id}</span>
                        </div>
                        <div className="font-mono text-[10px] text-zinc-600 truncate">
                          {cp.correlation_id}
                        </div>
                      </button>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>

          <div className="flex flex-col overflow-auto">
            {selected ? (
              <CheckpointDetail
                checkpoint={selected}
                onRespond={() => setRespondTarget(selected)}
                onCancel={() => setCancelTarget(selected)}
              />
            ) : (
              <div className="flex h-full items-center justify-center p-6 text-center text-sm text-zinc-500">
                Select a checkpoint to respond or cancel.
              </div>
            )}
          </div>
        </div>
      </div>

      <CheckpointRespondDialog
        checkpoint={respondTarget}
        open={respondTarget !== null}
        onOpenChange={(open) => {
          if (!open) setRespondTarget(null)
        }}
        onResponded={() => {
          setSelected(null)
          fetchPending()
        }}
      />
      <CheckpointCancelDialog
        checkpoint={cancelTarget}
        open={cancelTarget !== null}
        onOpenChange={(open) => {
          if (!open) setCancelTarget(null)
        }}
        onCanceled={() => {
          setSelected(null)
          fetchPending()
        }}
      />
    </div>
  )
}

interface DetailProps {
  checkpoint: Checkpoint
  onRespond: () => void
  onCancel: () => void
}

function CheckpointDetail({ checkpoint, onRespond, onCancel }: DetailProps) {
  return (
    <div className="flex flex-col">
      <div className="flex items-center justify-between gap-3 border-b border-zinc-800/80 px-6 py-4">
        <div className="flex flex-col gap-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="inline-block rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-amber-200">
              {checkpoint.type}
            </span>
            <span className="text-[10px] uppercase tracking-wider text-zinc-500">
              {checkpoint.status}
            </span>
          </div>
          <CopyableId id={checkpoint.correlation_id} />
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={onCancel}>
            Cancel
          </Button>
          <Button size="sm" onClick={onRespond}>
            Respond
          </Button>
        </div>
      </div>

      <div className="flex flex-col gap-4 p-6">
        <DetailField label="Task">
          <Link
            to={`/tasks/${checkpoint.task_id}`}
            className="font-mono text-xs text-zinc-200 hover:text-white underline-offset-4 hover:underline"
          >
            {checkpoint.task_id}
          </Link>
        </DetailField>

        <DetailField label="Emitter">
          <span className="font-mono text-xs text-zinc-300">
            {checkpoint.emitter_source_type}
            {checkpoint.emitter_source_ref ? ` · ${checkpoint.emitter_source_ref}` : ''}
          </span>
        </DetailField>

        <DetailField label="Emitted">
          <span className="text-xs text-zinc-300">
            {new Date(checkpoint.emitted_at).toLocaleString()}
            <span className="ml-2 text-zinc-500">({elapsed(checkpoint.emitted_at)})</span>
          </span>
        </DetailField>

        {checkpoint.timeout_at && (
          <DetailField label="Timeout at">
            <span className="text-xs text-zinc-300">
              {new Date(checkpoint.timeout_at).toLocaleString()}
            </span>
          </DetailField>
        )}

        <DetailField label="Payload">
          <pre className="whitespace-pre-wrap rounded-md border border-zinc-800 bg-zinc-950 p-3 font-mono text-xs text-zinc-300 max-h-[50vh] overflow-auto">
            {formatPayload(checkpoint.payload_json)}
          </pre>
        </DetailField>
      </div>
    </div>
  )
}

function DetailField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-[10px] uppercase tracking-wider text-zinc-500">{label}</span>
      <div>{children}</div>
    </div>
  )
}
