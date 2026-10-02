import { Button, Input } from '@hollis-labs/sysop-ui'
import type { ParentQuery } from '@/lib/api'
import { ScopePicker, type ScopeKind } from './scope-picker'

const selectClass = 'h-8 rounded-md border border-border bg-background px-2 text-sm'
export function ParentPageControls({ kind, query, onChange, pageStart, hasNext, loading, stale, previous, next, refresh }: {
  kind: ScopeKind; query: ParentQuery; onChange: (query: ParentQuery) => void
  pageStart: number; hasNext: boolean; loading: boolean; stale: boolean
  previous: () => void; next: () => void; refresh: () => void
}) {
  const update = (patch: Partial<ParentQuery>) => onChange({ ...query, ...patch })
  return <div className="flex flex-wrap items-center gap-3 border-b border-border px-6 py-3">
    <Input aria-label={`Search ${kind}s`} placeholder={`Search ${kind}s…`} className="h-8 w-56" value={query.search ?? ''} onChange={event => update({ search: event.target.value })} />
    <select aria-label="Status" className={selectClass} value={query.status ?? ''} onChange={event => update({ status: event.target.value || undefined })}><option value="">All statuses</option><option value="active">Active</option><option value="inactive">Inactive</option>{kind === 'sprint' && <option value="completed">Completed</option>}</select>
    {kind !== 'project' && <ScopePicker kind="project" label="Filter project" placeholder="All projects" value={query.project_id} onChange={id => update({ project_id: id ?? undefined })} className="h-8 w-48 justify-between" />}
    <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={query.include_archived ?? false} onChange={event => update({ include_archived: event.target.checked })} />Include archived</label>
    <select aria-label="Sort" className={selectClass} value={query.sort_by} onChange={event => update({ sort_by: event.target.value })}><option value="name">Name</option><option value="status">Status</option><option value="updated_at">Updated</option><option value="created_at">Created</option></select>
    <select aria-label="Direction" className={selectClass} value={query.sort_dir} onChange={event => update({ sort_dir: event.target.value as 'asc' | 'desc' })}><option value="asc">Ascending</option><option value="desc">Descending</option></select>
    <Button size="sm" variant="outline" onClick={refresh} disabled={loading}>Refresh</Button>
    <div className="ml-auto flex items-center gap-2 text-xs"><Button size="sm" variant="outline" onClick={previous} disabled={loading || pageStart === 0}>Previous</Button><span>Page {Math.floor(pageStart / 50) + 1}</span><Button size="sm" variant="outline" onClick={next} disabled={loading || !hasNext || stale}>Next</Button></div>
    {stale && <p role="status" className="w-full text-xs text-muted-foreground">Scopes changed. Refresh to update the row page; counts are refreshed automatically.</p>}
  </div>
}
