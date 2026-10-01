import { Button } from '@hollis-labs/sysop-ui'

interface PageControls {
  items: unknown[]
  total?: number
  hasMore: boolean
  loading: boolean
  error: Error | null
  isStale: boolean
  loadMore: () => Promise<void>
  reload: () => Promise<void>
}

/** Counts describe loaded rows; continuation is always an explicit action. */
export function ListPageControls({ page, label = 'rows' }: { page: PageControls; label?: string }) {
  return <div className="flex flex-wrap items-center gap-3 px-4 py-3 text-xs text-muted-foreground">
    <span>{page.items.length} {page.total === undefined ? `${label} loaded` : `of ${page.total} ${label} loaded`}</span>
    {page.isStale && <span>Changes available</span>}
    {page.error && <span role="alert">{page.error.message}</span>}
    {page.hasMore && <Button size="sm" variant="outline" disabled={page.loading} onClick={() => void page.loadMore()}>
      {page.loading ? 'Loading…' : `Load more ${label}`}
    </Button>}
    <Button size="sm" variant="ghost" disabled={page.loading} onClick={() => void page.reload()}>Refresh {label}</Button>
  </div>
}
