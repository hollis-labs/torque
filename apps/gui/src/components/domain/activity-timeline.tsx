import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import {
  AlertCircle,
  CheckCircle2,
  Loader2,
  MessageSquare,
  Package,
  Play,
} from 'lucide-react'
import { EmptyState } from '@hollis-labs/sysop-ui'
import { formatCost, formatTokens, formatRelativeTime } from '@/lib/utils'
import { useActiveRun, type ActivityItem } from '@/hooks/active-runs-context'
import type { Run, Comment, Artifact } from '@/lib/types'

type Kind = 'run' | 'comment' | 'artifact' | 'live'

interface TimelineEvent {
  id: string
  kind: Kind
  at: string
  icon: React.ReactNode
  node: React.ReactNode
}

function runIcon(status: string) {
  switch (status) {
    case 'running':
      return <Loader2 className="h-3.5 w-3.5 animate-spin text-amber-400" aria-hidden />
    case 'completed':
      return <CheckCircle2 className="h-3.5 w-3.5 text-emerald-400" aria-hidden />
    case 'failed':
      return <AlertCircle className="h-3.5 w-3.5 text-red-400" aria-hidden />
    default:
      return <Play className="h-3.5 w-3.5 text-zinc-400" aria-hidden />
  }
}

function RunNode({ run }: { run: Run }) {
  const total = (run.prompt_tokens ?? 0) + (run.completion_tokens ?? 0)
  return (
    <div className="flex flex-col gap-0.5">
      <div className="flex items-center gap-2">
        <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
          run #{run.id} · {run.status}
        </span>
        {run.executor && (
          <span className="text-[10px] text-zinc-600">{run.executor}</span>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-3 text-[11px] text-zinc-400 tabular-nums">
        {total > 0 && <span>{formatTokens(total)} tokens</span>}
        {run.cost > 0 && <span className="text-zinc-300">{formatCost(run.cost)}</span>}
        {run.exit_code !== 0 && (
          <span className="font-mono text-red-400">exit {run.exit_code}</span>
        )}
      </div>
      {run.error_message && (
        <p className="mt-1 rounded bg-red-950/30 px-2 py-1 text-[11px] text-red-300 font-mono">
          {run.error_message}
        </p>
      )}
    </div>
  )
}

function CommentNode({ comment }: { comment: Comment }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
        comment · {comment.author || 'anon'}
      </span>
      <p className="text-[12px] text-zinc-200 whitespace-pre-wrap">{comment.content}</p>
    </div>
  )
}

function ArtifactNode({ artifact }: { artifact: Artifact }) {
  const label = artifact.file_path || artifact.url || artifact.content || 'artifact'
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
        artifact · {artifact.type}
      </span>
      {artifact.url ? (
        <Link
          to={artifact.url}
          className="text-[12px] font-mono text-sky-400 hover:text-sky-300 break-all"
          target="_blank"
          rel="noreferrer"
        >
          {label}
        </Link>
      ) : (
        <span className="text-[12px] font-mono text-zinc-300 break-all">{label}</span>
      )}
    </div>
  )
}

function LiveNode({ item }: { item: ActivityItem }) {
  if (item.kind === 'note') {
    return (
      <div className="flex flex-col gap-0.5">
        <span className="text-[10px] uppercase tracking-[.18em] text-amber-300/80">
          live · note
        </span>
        <p className="text-[12px] text-zinc-200 whitespace-pre-wrap">{item.text ?? ''}</p>
      </div>
    )
  }
  if (item.kind === 'tokens' && item.tokens) {
    return (
      <div className="flex flex-col gap-0.5">
        <span className="text-[10px] uppercase tracking-[.18em] text-amber-300/80">
          live · tokens
        </span>
        <span className="text-[12px] text-zinc-300 tabular-nums">
          prompt {item.tokens.prompt.toLocaleString()} · completion{' '}
          {item.tokens.completion.toLocaleString()} · {formatCost(item.tokens.cost)}
        </span>
      </div>
    )
  }
  if (item.kind === 'tool_use' && item.tool_use) {
    return (
      <div className="flex flex-col gap-0.5">
        <span className="text-[10px] uppercase tracking-[.18em] text-amber-300/80">
          live · {item.tool_use.tool_name}
        </span>
        {item.tool_use.args_summary && (
          <span className="text-[11px] font-mono text-zinc-400 break-all">
            {item.tool_use.args_summary}
          </span>
        )}
      </div>
    )
  }
  if (item.kind === 'artifact' && item.artifact) {
    const label = item.artifact.file_path || item.artifact.url || item.artifact.content || 'artifact'
    return (
      <div className="flex flex-col gap-0.5">
        <span className="text-[10px] uppercase tracking-[.18em] text-amber-300/80">
          live · {item.artifact.artifact_type}
        </span>
        <span className="text-[12px] font-mono text-zinc-300 break-all">{label}</span>
      </div>
    )
  }
  return null
}

interface ActivityTimelineProps {
  taskId: string
  runs: Run[] | null
  comments: Comment[] | null
  artifacts: Artifact[] | null
}

const COMMENT_ICON = <MessageSquare className="h-3.5 w-3.5 text-zinc-400" aria-hidden />
const ARTIFACT_ICON = <Package className="h-3.5 w-3.5 text-emerald-400" aria-hidden />
const LIVE_ICON = <Loader2 className="h-3.5 w-3.5 animate-spin text-amber-400" aria-hidden />

export function ActivityTimeline({ taskId, runs, comments, artifacts }: ActivityTimelineProps) {
  const activeRun = useActiveRun(taskId)

  const events: TimelineEvent[] = useMemo(() => {
    const out: TimelineEvent[] = []

    for (const run of runs ?? []) {
      out.push({
        id: `run-${run.id}`,
        kind: 'run',
        at: run.completed_at || run.started_at,
        icon: runIcon(run.status),
        node: <RunNode run={run} />,
      })
    }

    for (const comment of comments ?? []) {
      out.push({
        id: `comment-${comment.id}`,
        kind: 'comment',
        at: comment.created_at,
        icon: COMMENT_ICON,
        node: <CommentNode comment={comment} />,
      })
    }

    for (const artifact of artifacts ?? []) {
      out.push({
        id: `artifact-${artifact.id}`,
        kind: 'artifact',
        at: artifact.created_at,
        icon: ARTIFACT_ICON,
        node: <ArtifactNode artifact={artifact} />,
      })
    }

    out.sort((a, b) => new Date(b.at).getTime() - new Date(a.at).getTime())
    return out
  }, [runs, comments, artifacts])

  const liveEvents: TimelineEvent[] = useMemo(() => {
    if (!activeRun) return []
    return activeRun.feed.map((item) => ({
      id: `live-${item.id}`,
      kind: 'live' as const,
      at: item.at,
      icon: LIVE_ICON,
      node: <LiveNode item={item} />,
    }))
  }, [activeRun])

  const loading = runs === null && comments === null && artifacts === null
  const combined = [...liveEvents, ...events]

  if (loading) {
    return (
      <div className="flex flex-col gap-3">
        <div className="h-16 animate-pulse rounded-md bg-zinc-900/60" />
        <div className="h-16 animate-pulse rounded-md bg-zinc-900/60" />
      </div>
    )
  }

  if (combined.length === 0) {
    return (
      <EmptyState
        variant="no-results"
        title="No activity yet"
        description="Runs, comments, and artifacts will appear here as they happen."
      />
    )
  }

  return (
    <ol className="relative flex flex-col">
      <div
        aria-hidden
        className="absolute left-[7px] top-2 bottom-2 w-px bg-zinc-800"
      />
      {combined.map((ev) => (
        <li key={ev.id} className="flex gap-3 py-2">
          <div className="relative z-10 flex flex-none items-start pt-0.5">
            <div className="flex h-4 w-4 items-center justify-center rounded-full border border-zinc-800 bg-zinc-950">
              {ev.icon}
            </div>
          </div>
          <div className="flex-1 min-w-0 rounded-md border border-zinc-800/60 bg-zinc-950/50 px-3 py-2">
            {ev.node}
            <div className="mt-1 text-[10px] text-zinc-600" title={ev.at}>
              {formatRelativeTime(ev.at)}
            </div>
          </div>
        </li>
      ))}
    </ol>
  )
}
