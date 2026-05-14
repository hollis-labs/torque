import {
  Activity,
  Coins,
  Eye,
  FileText,
  ListTodo,
  MessageSquare,
  Package,
  Pencil,
  Search,
  Terminal,
  Wrench,
} from 'lucide-react'
import { useActiveRun, type ActivityItem } from '@/hooks/active-runs-context'
import { useElapsed } from '@/hooks/use-elapsed'

// renderToolIcon returns a JSX element for the given tool name. Matching is
// case-insensitive so FE renders the right glyph whether the agent calls
// "Bash" or "bash". Unknown tools fall back to a wrench so the row is still
// obviously a tool invocation. Returns a ready-to-render element (instead of
// a component reference) so the react-hooks/static-components rule stays
// happy.
function renderToolIcon(name: string, className: string) {
  switch (name.toLowerCase()) {
    case 'edit':
    case 'write':
    case 'multiedit':
      return <Pencil className={className} />
    case 'bash':
    case 'shell':
      return <Terminal className={className} />
    case 'read':
      return <Eye className={className} />
    case 'grep':
    case 'glob':
      return <Search className={className} />
    case 'todowrite':
      return <ListTodo className={className} />
    case 'task':
    case 'agent':
      return <MessageSquare className={className} />
    default:
      return <Wrench className={className} />
  }
}

interface ActivityPanelProps {
  taskId: string
}

function formatElapsed(seconds: number): string {
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`
}

function formatCost(cost: number): string {
  if (!Number.isFinite(cost) || cost === 0) return '$0.00'
  if (cost < 0.01) return `$${cost.toFixed(4)}`
  return `$${cost.toFixed(2)}`
}

function ItemRow({ item }: { item: ActivityItem }) {
  const ts = new Date(item.at).toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })

  if (item.kind === 'note') {
    return (
      <div className="flex gap-2 border-b border-zinc-800/40 px-3 py-1.5 last:border-b-0">
        <FileText className="mt-0.5 h-3 w-3 flex-none text-zinc-500" />
        <div className="min-w-0 flex-1">
          <div className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
            note · {ts}
          </div>
          <div className="text-[12px] text-zinc-200 whitespace-pre-wrap">
            {item.text ?? ''}
          </div>
        </div>
      </div>
    )
  }

  if (item.kind === 'artifact' && item.artifact) {
    return (
      <div className="flex gap-2 border-b border-zinc-800/40 px-3 py-1.5 last:border-b-0">
        <Package className="mt-0.5 h-3 w-3 flex-none text-emerald-500" />
        <div className="min-w-0 flex-1">
          <div className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
            {item.artifact.artifact_type} · {ts}
          </div>
          {item.artifact.file_path && (
            <div className="font-mono text-[11px] text-zinc-300 break-all">
              {item.artifact.file_path}
            </div>
          )}
          {item.artifact.url && !item.artifact.file_path && (
            <div className="font-mono text-[11px] text-blue-400 break-all">
              {item.artifact.url}
            </div>
          )}
          {item.artifact.content && (
            <pre className="mt-1 max-h-24 overflow-auto rounded bg-zinc-900/60 p-1.5 text-[11px] text-zinc-300 whitespace-pre-wrap">
              {item.artifact.content}
            </pre>
          )}
        </div>
      </div>
    )
  }

  if (item.kind === 'tokens' && item.tokens) {
    return (
      <div className="flex gap-2 border-b border-zinc-800/40 px-3 py-1.5 last:border-b-0">
        <Coins className="mt-0.5 h-3 w-3 flex-none text-amber-500" />
        <div className="min-w-0 flex-1">
          <div className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
            tokens · {ts}
          </div>
          <div className="text-[12px] text-zinc-300">
            prompt {item.tokens.prompt.toLocaleString()} · completion{' '}
            {item.tokens.completion.toLocaleString()} ·{' '}
            {formatCost(item.tokens.cost)}
          </div>
        </div>
      </div>
    )
  }

  if (item.kind === 'tool_use' && item.tool_use) {
    return (
      <div className="flex gap-2 border-b border-zinc-800/40 px-3 py-1.5 last:border-b-0">
        {renderToolIcon(
          item.tool_use.tool_name,
          'mt-0.5 h-3 w-3 flex-none text-sky-400',
        )}
        <div className="min-w-0 flex-1">
          <div className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
            {item.tool_use.tool_name} · {ts}
          </div>
          {item.tool_use.args_summary && (
            <div className="font-mono text-[11px] text-zinc-300 break-all">
              {item.tool_use.args_summary}
            </div>
          )}
        </div>
      </div>
    )
  }

  return null
}

// ActivityPanel renders a live feed of the in-flight run for this task:
// elapsed timer, optional cost meter, and a reverse-chronological list of
// TORQUE_NOTE / TORQUE_ARTIFACT / TORQUE_TOKENS events as they
// stream in. Renders nothing when the task has no active run, so consumers
// can drop it in unconditionally.
export function ActivityPanel({ taskId }: ActivityPanelProps) {
  const run = useActiveRun(taskId)
  const elapsed = useElapsed(run?.startedAt ?? null)

  if (!run) return null

  return (
    <div
      className="mx-4 mt-3 rounded-md border border-amber-900/40 bg-amber-950/20"
      role="status"
      aria-label="Run in progress"
      aria-live="polite"
    >
      <div className="flex items-center gap-3 border-b border-amber-900/30 px-3 py-2">
        <span className="relative inline-flex h-2 w-2 items-center justify-center">
          <span className="absolute inset-0 animate-ping rounded-full bg-amber-400/80" />
          <span className="relative inline-block h-1.5 w-1.5 rounded-full bg-amber-400" />
        </span>
        <Activity className="h-3.5 w-3.5 text-amber-400" />
        <span className="text-[10px] uppercase tracking-[.18em] text-amber-200">
          Run in progress
        </span>
        <span className="ml-auto font-mono text-[12px] tabular-nums text-amber-100">
          {formatElapsed(elapsed)}
        </span>
        {run.lastTokens && (
          <span className="font-mono text-[11px] text-amber-200/80">
            {formatCost(run.lastTokens.cost)}
          </span>
        )}
      </div>

      {run.feed.length === 0 ? (
        <div className="px-3 py-3 text-[11px] italic text-zinc-500">
          {run.lastHeartbeatAt
            ? 'Agent working…'
            : "Waiting for the agent's first update…"}
        </div>
      ) : (
        <div className="max-h-72 overflow-auto">
          {run.feed.map((item) => (
            <ItemRow key={item.id} item={item} />
          ))}
        </div>
      )}
    </div>
  )
}
