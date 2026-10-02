import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ExternalLink, Pencil, Plus } from 'lucide-react'
import { Skeleton, Button, PageHeader, SummaryCards, EmptyState } from '@hollis-labs/sysop-ui'
import { ProjectCreateDialog } from '@/components/domain/project-create-dialog'
import { ScopeOverviewCard } from '@/components/domain/scope-overview-card'
import { useParentPage } from '@/hooks/use-parent-page'
import { ParentPageControls } from '@/components/domain/parent-page-controls'
import { rollupFromStatusCounts } from '@/lib/scope-metrics'
import type { Project } from '@/lib/types'

function PageSkeleton() {
  return (
    <div className="flex flex-col gap-4 p-6">
      <Skeleton className="h-40 w-full rounded-2xl" />
      <Skeleton className="h-40 w-full rounded-2xl" />
    </div>
  )
}

export default function ProjectsPage() {
  const navigate = useNavigate()
  const page = useParentPage<Project>('project')
  const { loading, error } = page
  const [createOpen, setCreateOpen] = useState(false)
  const totals = page.facets?.result
  function facetCount(dimension: string, value: string | number): number | string {
    const facet = totals?.facets.find(facet => facet.dimension === dimension)
    return facet?.buckets.find(bucket => bucket.value === value)?.count ?? (facet && !facet.truncated ? 0 : '—')
  }
  const summaryCards = [
    { label: 'Projects', value: totals?.matching_count ?? '—' },
    { label: 'Active', value: facetCount('status', 'active'), accentColor: '#34d399' },
    { label: 'Scoped Tasks', value: totals?.task_totals.total ?? '—', accentColor: '#60a5fa' },
    { label: 'Sprints', value: totals?.child_totals?.sprints ?? '—', accentColor: '#fbbf24' },
    { label: 'Epics', value: totals?.child_totals?.epics ?? '—', accentColor: '#a78bfa' },
  ]
  const projectCards = page.items.map(project => {
    const scope = page.rollups?.find(scope => scope.scope_id === project.id)
    return { project, rollup: scope ? rollupFromStatusCounts(scope.counts) : null, projectSprintCount: scope?.children?.sprints ?? '—', projectEpicCount: scope?.children?.epics ?? '—' }
  })

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Projects">
        <Button variant="outline" size="sm" onClick={() => navigate('/operations')}>
          <ExternalLink className="h-3.5 w-3.5" />
          Operations
        </Button>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          New Project
        </Button>
      </PageHeader>
      <SummaryCards cards={summaryCards} />
      {page.facets?.error && <p role="alert" className="px-6 text-sm text-destructive">Counts unavailable: {page.facets.error}</p>}
      <ParentPageControls kind="project" query={page.query} onChange={page.changeQuery} pageStart={page.pageStart} hasNext={page.hasNext} loading={loading} stale={page.isStale} previous={page.previousPage} next={() => void page.nextPage()} refresh={page.refresh} />

      <ProjectCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(project) => navigate(`/projects/${project.id}`)}
      />

      <div className="flex-1 overflow-auto">
        {loading ? (
          <PageSkeleton />
        ) : error ? (
          <div className="p-6">
            <EmptyState variant="error" title="Something went wrong" description={error.message} action={{ label: 'Retry', onClick: page.refresh }} />
          </div>
        ) : projectCards.length === 0 ? (
          <div className="p-6">
            <EmptyState
              variant="empty"
              title="No matching projects"
              description="Try a different search, status or archive filter."
              action={{ label: 'Create project', onClick: () => setCreateOpen(true) }}
            />
          </div>
        ) : (
          <div className="flex flex-col gap-4 p-6">
            {projectCards.map(({ project, rollup, projectSprintCount, projectEpicCount }) => (
              <ScopeOverviewCard
                key={project.id}
                kindLabel="Project"
                title={project.name}
                to={`/projects/${project.id}`}
                id={project.id}
                status={project.status}
                description={project.description}
                progress={rollup}
                metrics={[
                  { label: 'Open', value: rollup?.open ?? '—' },
                  { label: 'Doing', value: rollup?.doing ?? '—', accentColor: '#60a5fa' },
                  { label: 'Blocked', value: rollup?.blocked ?? '—', accentColor: '#f87171' },
                  { label: 'Done', value: rollup?.done ?? '—', accentColor: '#34d399' },
                ]}
                meta={[
                  { label: 'Repo Path', value: <span className="font-mono text-xs text-zinc-300">{project.repo_path || 'Not set'}</span> },
                  { label: 'Sprints', value: projectSprintCount },
                  { label: 'Epics', value: projectEpicCount },
                  { label: 'Updated', value: new Date(project.updated_at).toLocaleDateString() },
                ]}
                actions={
                  <>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/projects/${project.id}/edit`)}>
                      <Pencil className="h-3.5 w-3.5" />
                      Edit
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => navigate(`/operations?project_id=${project.id}`)}>
                      <ExternalLink className="h-3.5 w-3.5" />
                      Tasks
                    </Button>
                  </>
                }
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
