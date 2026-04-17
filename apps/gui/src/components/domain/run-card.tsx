import { Link } from 'react-router-dom'
import { Clock, Cpu, DollarSign, AlertCircle, CheckCircle2, Loader2, CalendarClock } from 'lucide-react'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { formatCost, formatTokens, formatDuration } from '@/lib/utils'
import type { Run } from '@/lib/types'

function formatTimestamp(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

interface RunCardProps {
  run: Run
  showTaskLink?: boolean
}

function RunStatusBadge({ status }: { status: string }) {
  switch (status) {
    case 'running':
      return (
        <Badge variant="outline" className="gap-1.5">
          <Loader2 className="h-3 w-3 animate-spin text-[var(--color-status-doing)]" />
          Running
        </Badge>
      )
    case 'completed':
      return (
        <Badge variant="outline" className="gap-1.5">
          <CheckCircle2 className="h-3 w-3 text-[var(--color-status-done)]" />
          Completed
        </Badge>
      )
    case 'failed':
      return (
        <Badge variant="outline" className="gap-1.5">
          <AlertCircle className="h-3 w-3 text-[var(--color-status-blocked)]" />
          Failed
        </Badge>
      )
    default:
      return <Badge variant="secondary">{status}</Badge>
  }
}

export function RunCard({ run, showTaskLink }: RunCardProps) {
  const duration =
    run.completed_at && run.started_at
      ? formatDuration(new Date(run.completed_at).getTime() - new Date(run.started_at).getTime())
      : null

  const totalTokens = (run.prompt_tokens ?? 0) + (run.completion_tokens ?? 0)

  return (
    <Card className="gap-3">
      <CardHeader>
        <div className="flex items-start justify-between gap-3">
          <div className="flex flex-col gap-1 min-w-0">
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs text-muted-foreground">#{run.id}</span>
              {showTaskLink && (
                <Link
                  to={`/tasks/${run.task_id}`}
                  className="text-sm font-medium text-foreground hover:text-primary hover:underline underline-offset-4 truncate"
                >
                  Task {run.task_id}
                </Link>
              )}
            </div>
            <span className="text-sm text-muted-foreground truncate">
              {run.executor || run.agent_profile || 'Unknown executor'}
            </span>
          </div>
          <RunStatusBadge status={run.status} />
        </div>
      </CardHeader>
      <CardContent>
        <div className="flex flex-wrap gap-4 text-xs text-muted-foreground">
          {totalTokens > 0 && (
            <span className="flex items-center gap-1">
              <Cpu className="h-3 w-3" />
              {formatTokens(totalTokens)} tokens
            </span>
          )}
          {run.cost > 0 && (
            <span className="flex items-center gap-1">
              <DollarSign className="h-3 w-3" />
              {formatCost(run.cost)}
            </span>
          )}
          {duration && (
            <span className="flex items-center gap-1">
              <Clock className="h-3 w-3" />
              {duration}
            </span>
          )}
          {run.completed_at && (
            <span className="flex items-center gap-1" title={`Ended ${run.completed_at}`}>
              <CalendarClock className="h-3 w-3" />
              {formatTimestamp(run.completed_at)}
            </span>
          )}
          {run.exit_code !== 0 && (
            <span className="flex items-center gap-1 font-mono text-destructive">
              exit {run.exit_code}
            </span>
          )}
        </div>
        {run.error_message && (
          <p className="mt-2 rounded-md bg-destructive/10 px-2.5 py-1.5 text-xs text-destructive font-mono">
            {run.error_message}
          </p>
        )}
      </CardContent>
    </Card>
  )
}
