import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ExternalLink, Pencil, Plus } from 'lucide-react'
import { Skeleton, Button, PageHeader, SummaryCards, EmptyState } from '@hollis-labs/sysop-ui'
import { EpicCreateDialog } from '@/components/domain/epic-create-dialog'
import { ScopeName, ScopeNames } from '@/components/domain/scope-name'
import { ScopeOverviewCard } from '@/components/domain/scope-overview-card'
import { useParentPage } from '@/hooks/use-parent-page'
import { ParentPageControls } from '@/components/domain/parent-page-controls'
import { rollupFromStatusCounts } from '@/lib/scope-metrics'
import type { Epic } from '@/lib/types'

function PageSkeleton() {
  return (
    <div className="flex flex-col gap-4 p-6">
      <Skeleton className="h-40 w-full rounded-2xl" />
      <Skeleton className="h-40 w-full rounded-2xl" />
    </div>
  )
}

export default function EpicsPage() {
  const navigate = useNavigate()
  const page = useParentPage<Epic>('epic')
  const { loading, error } = page
  const [createOpen, setCreateOpen] = useState(false)
  const totals = page.facets?.result
  function facetCount(dimension: string, value: string | number): number | string {
    const facet = totals?.facets.find(facet => facet.dimension === dimension)
    return facet?.buckets.find(bucket => bucket.value === value)?.count ?? (facet && !facet.truncated ? 0 : '—')
  }
  const summaryCards = [
    { label: 'Epics', value: totals?.matching_count ?? '—' },
    { label: 'Active', value: facetCount('status', 'active'), accentColor: '#34d399' },
    { label: 'Scoped Tasks', value: totals?.task_totals.total ?? '—', accentColor: '#a78bfa' },
    { label: 'P1', value: facetCount('priority', 1), accentColor: '#f87171' },
  ]
  const epicCards = page.items.map(epic => {
    const scope = page.rollups?.find(scope => scope.scope_id === epic.id)
    return { epic, rollup: scope ? rollupFromStatusCounts(scope.counts) : null }
  })

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Epics">
        <Button variant="outline" size="sm" onClick={() => navigate('/operations')}>
          <ExternalLink className="h-3.5 w-3.5" />
          Operations
        </Button>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          New Epic
        </Button>
      </PageHeader>
      <SummaryCards cards={summaryCards} />
      {page.facets?.error && <p role="alert" className="px-6 text-sm text-destructive">Counts unavailable: {page.facets.error}</p>}
      <ParentPageControls kind="epic" query={page.query} onChange={page.changeQuery} pageStart={page.pageStart} hasNext={page.hasNext} loading={loading} stale={page.isStale} previous={page.previousPage} next={() => void page.nextPage()} refresh={page.refresh} />

      <EpicCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(epic) => navigate(`/epics/${epic.id}`)}
      />

      <div className="flex-1 overflow-auto">
        {loading ? (
          <PageSkeleton />
        ) : error ? (
          <div className="p-6">
            <EmptyState variant="error" title="Something went wrong" description={error.message} action={{ label: 'Retry', onClick: page.refresh }} />
          </div>
        ) : epicCards.length === 0 ? (
          <div className="p-6">
            <EmptyState
              variant="empty"
              title="No matching epics"
              description="Try a different search, status or archive filter."
              action={{ label: 'Create epic', onClick: () => setCreateOpen(true) }}
            />
          </div>
        ) : (
          <div className="flex flex-col gap-4 p-6">
            <ScopeNames kind="project" ids={page.items.flatMap(item => item.project_id ? [item.project_id] : [])}>
            {epicCards.map(({ epic, rollup }) => (
              <ScopeOverviewCard
                key={epic.id}
                kindLabel="Epic"
                title={epic.name}
                to={`/epics/${epic.id}`}
                id={epic.id}
                status={epic.status}
                description={epic.description}
                progress={rollup}
                metrics={[
                  { label: 'Open', value: rollup?.open ?? '—' },
                  { label: 'Doing', value: rollup?.doing ?? '—', accentColor: '#60a5fa' },
                  { label: 'Blocked', value: rollup?.blocked ?? '—', accentColor: '#f87171' },
                  { label: 'Done', value: rollup?.done ?? '—', accentColor: '#34d399' },
                ]}
                meta={[
                  { label: 'Project', value: epic.project_id ? <ScopeName kind="project" id={epic.project_id} /> : 'None' },
                  { label: 'Priority', value: epic.priority === null ? 'None' : `P${epic.priority}` },
                  { label: 'Updated', value: new Date(epic.updated_at).toLocaleDateString() },
                ]}
                actions={
                  <>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/epics/${epic.id}/edit`)}>
                      <Pencil className="h-3.5 w-3.5" />
                      Edit
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/operations?epic_id=${epic.id}`)}>
                      <ExternalLink className="h-3.5 w-3.5" />
                      Tasks
                    </Button>
                  </>
                }
              />
            ))}
            </ScopeNames>
          </div>
        )}
      </div>
    </div>
  )
}
