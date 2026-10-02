import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ExternalLink, Pencil, Plus } from 'lucide-react'
import { Skeleton, Button, PageHeader, SummaryCards, EmptyState } from '@hollis-labs/sysop-ui'
import { SprintCreateDialog } from '@/components/domain/sprint-create-dialog'
import { ScopeName, ScopeNames } from '@/components/domain/scope-name'
import { ScopeOverviewCard } from '@/components/domain/scope-overview-card'
import { useParentPage } from '@/hooks/use-parent-page'
import { ParentPageControls } from '@/components/domain/parent-page-controls'
import { rollupFromStatusCounts } from '@/lib/scope-metrics'
import type { Sprint } from '@/lib/types'

function PageSkeleton() {
  return (
    <div className="flex flex-col gap-4 p-6">
      <Skeleton className="h-40 w-full rounded-2xl" />
      <Skeleton className="h-40 w-full rounded-2xl" />
    </div>
  )
}

export default function SprintsPage() {
  const navigate = useNavigate()
  const page = useParentPage<Sprint>('sprint')
  const { loading, error } = page
  const [createOpen, setCreateOpen] = useState(false)
  const totals = page.facets?.result
  function facetCount(dimension: string, value: string | number): number | string {
    const facet = totals?.facets.find(facet => facet.dimension === dimension)
    return facet?.buckets.find(bucket => bucket.value === value)?.count ?? (facet && !facet.truncated ? 0 : '—')
  }
  const summaryCards = [
    { label: 'Sprints', value: totals?.matching_count ?? '—' },
    { label: 'Active', value: facetCount('status', 'active'), accentColor: '#34d399' },
    { label: 'Completed', value: facetCount('status', 'completed'), accentColor: '#60a5fa' },
    { label: 'Scoped Tasks', value: totals?.task_totals.total ?? '—', accentColor: '#fbbf24' },
  ]
  const sprintCards = page.items.map(sprint => {
    const scope = page.rollups?.find(scope => scope.scope_id === sprint.id)
    return { sprint, rollup: scope ? rollupFromStatusCounts(scope.counts) : null }
  })

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Sprints">
        <Button variant="outline" size="sm" onClick={() => navigate('/operations')}>
          <ExternalLink className="h-3.5 w-3.5" />
          Operations
        </Button>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          New Sprint
        </Button>
      </PageHeader>
      <SummaryCards cards={summaryCards} />
      {page.facets?.error && <p role="alert" className="px-6 text-sm text-destructive">Counts unavailable: {page.facets.error}</p>}
      <ParentPageControls kind="sprint" query={page.query} onChange={page.changeQuery} pageStart={page.pageStart} hasNext={page.hasNext} loading={loading} stale={page.isStale} previous={page.previousPage} next={() => void page.nextPage()} refresh={page.refresh} />

      <SprintCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(sprint) => navigate(`/sprints/${sprint.id}`)}
      />

      <div className="flex-1 overflow-auto">
        {loading ? (
          <PageSkeleton />
        ) : error ? (
          <div className="p-6">
            <EmptyState variant="error" title="Something went wrong" description={error.message} action={{ label: 'Retry', onClick: page.refresh }} />
          </div>
        ) : sprintCards.length === 0 ? (
          <div className="p-6">
            <EmptyState
              variant="empty"
              title="No matching sprints"
              description="Try a different search, status or archive filter."
              action={{ label: 'Create sprint', onClick: () => setCreateOpen(true) }}
            />
          </div>
        ) : (
          <div className="flex flex-col gap-4 p-6">
            <ScopeNames kind="project" ids={page.items.flatMap(item => item.project_id ? [item.project_id] : [])}>
            {sprintCards.map(({ sprint, rollup }) => (
              <ScopeOverviewCard
                key={sprint.id}
                kindLabel="Sprint"
                title={sprint.name}
                to={`/sprints/${sprint.id}`}
                id={sprint.id}
                status={sprint.status}
                description={sprint.goal}
                progress={rollup}
                metrics={[
                  { label: 'Open', value: rollup?.open ?? '—' },
                  { label: 'Doing', value: rollup?.doing ?? '—', accentColor: '#60a5fa' },
                  { label: 'Review', value: rollup?.review ?? '—', accentColor: '#a78bfa' },
                  { label: 'Done', value: rollup?.done ?? '—', accentColor: '#34d399' },
                ]}
                meta={[
                  { label: 'Project', value: sprint.project_id ? <ScopeName kind="project" id={sprint.project_id} /> : 'None' },
                  { label: 'Approval', value: sprint.approval_mode || 'approve_each' },
                  { label: 'Budget', value: sprint.cost_budget === null ? 'None' : `$${sprint.cost_budget.toFixed(2)}` },
                  { label: 'Updated', value: new Date(sprint.updated_at).toLocaleDateString() },
                ]}
                actions={
                  <>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/sprints/${sprint.id}/edit`)}>
                      <Pencil className="h-3.5 w-3.5" />
                      Edit
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/operations?sprint_id=${sprint.id}`)}>
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
