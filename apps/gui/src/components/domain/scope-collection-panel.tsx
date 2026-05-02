import { Link } from 'react-router-dom'
import { ProgressBar } from '@/components/domain/progress-bar'
import { StatusBadge } from '@/components/domain/status-badge'

interface ScopeCollectionItem {
  id: string
  title: string
  to: string
  status: string
  subtitle?: string
  progress?: {
    total: number
    done: number
    completion: number
  }
}

interface ScopeCollectionPanelProps {
  title: string
  items: ScopeCollectionItem[]
  emptyMessage: string
}

export function ScopeCollectionPanel({ title, items, emptyMessage }: ScopeCollectionPanelProps) {
  return (
    <section className="rounded-2xl border border-zinc-800/80 bg-zinc-950/70">
      <div className="flex items-center justify-between border-b border-zinc-800/80 px-5 py-4">
        <div>
          <h2 className="text-sm font-semibold uppercase tracking-[0.18em] text-zinc-400">{title}</h2>
          <p className="mt-1 text-sm text-zinc-500">{items.length}</p>
        </div>
      </div>
      {items.length === 0 ? (
        <div className="px-5 py-8 text-sm text-zinc-500">{emptyMessage}</div>
      ) : (
        <div className="divide-y divide-zinc-800/70">
          {items.map((item) => (
            <div key={item.id} className="grid gap-3 px-5 py-4 xl:grid-cols-[minmax(0,1fr)_220px] xl:items-center">
              <div className="min-w-0">
                <div className="mb-2 flex flex-wrap items-center gap-2">
                  <Link to={item.to} className="truncate text-base font-medium text-zinc-100 hover:text-zinc-300">
                    {item.title}
                  </Link>
                  <StatusBadge status={item.status} />
                </div>
                <div className="font-mono text-[11px] uppercase tracking-[0.18em] text-zinc-600">{item.id}</div>
                {item.subtitle && <div className="mt-2 text-sm text-zinc-400">{item.subtitle}</div>}
              </div>
              <div>
                {item.progress ? (
                  <>
                    <div className="mb-2 flex items-center justify-between text-[11px] uppercase tracking-[0.18em] text-zinc-500">
                      <span>Progress</span>
                      <span className="font-mono text-zinc-400">
                        {item.progress.done}/{item.progress.total}
                      </span>
                    </div>
                    <ProgressBar value={item.progress.completion} className="h-3 rounded-full bg-zinc-900" />
                  </>
                ) : (
                  <div className="text-sm text-zinc-500">No task progress yet.</div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
