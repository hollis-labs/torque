import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useListSearch } from './use-list-search'
import { useApi } from './use-api'
import { usePagedList, type ListChange } from './use-paged-list'
import type { ParentFacetResult, ParentQuery } from '@/lib/api'
import type { Epic, Project, Sprint } from '@/lib/types'
import type { ScopeKind } from '@/components/domain/scope-picker'

export const PARENT_PAGE_SIZE = 50
export function useParentPage<T extends Project | Epic | Sprint>(kind: ScopeKind) {
  const api = useApi()
  const [query, setQuery] = useState<ParentQuery>({ sort_by: kind === 'project' ? 'name' : 'updated_at', sort_dir: kind === 'project' ? 'asc' : 'desc' })
  const navigationGeneration = useRef(0)
  const fetchedPages = useRef(0)
  const [pageStart, setPageStart] = useState(0)
  const [revision, setRevision] = useState(0)
  const [facetState, setFacetState] = useState<{ key: string; result?: ParentFacetResult; error?: string } | null>(null)
  const [changed, setChanged] = useState(false)
  const [live, setLive] = useState('connecting')
  const invalidate = useCallback(() => { setRevision(n => n + 1); setChanged(true) }, [])
  const subscribe = useCallback((change: (event: ListChange<T>) => void) => api.subscribeEvents(event => {
    if (!event.type.startsWith(`${kind}.`) && !event.type.startsWith('task.') && !(kind === 'project' && (event.type.startsWith('sprint.') || event.type.startsWith('epic.')))) return
    const id = event.data[`${kind}_id`]
    change({ id: typeof id === 'string' ? id : '', refresh: true })
  }, setLive), [api, kind])
  const search = useListSearch(query.search ?? '')
  const params = useMemo(() => ({ ...query, search, limit: PARENT_PAGE_SIZE }), [query, search])
  const list = usePagedList<T, ParentQuery>({ params, queryKey: kind, getId: item => item.id, subscribe, onInvalidate: invalidate,
    fetchPage: async ({ params, cursor, signal }) => {
      const request = { ...params, ...(cursor ? { cursor } : {}) }
      const result = kind === 'project' ? await api.listProjects(params.status, request, signal) : kind === 'epic' ? await api.listEpics(request, signal) : await api.listSprints(request, signal)
      if (!signal.aborted) fetchedPages.current += 1
      return { ...result, items: result.items as T[] }
    },
  })
  // Keep the rendered page and its requested rollups bounded, while the shared
  // hook retains earlier user-requested pages for Back navigation.
  const items = list.items.slice(pageStart, pageStart + PARENT_PAGE_SIZE)
  const idsKey = JSON.stringify(items.map(item => item.id))
  const cohortKey = JSON.stringify({ status: query.status, search, project_id: query.project_id, include_archived: query.include_archived })
  const facetKey = `${kind}:${cohortKey}:${idsKey}`
  useEffect(() => {
    const abort = new AbortController()
    void api.parentFacets(kind, JSON.parse(cohortKey), JSON.parse(idsKey), abort.signal)
      .then(result => { if (!abort.signal.aborted) setFacetState({ key: facetKey, result }) })
      .catch((error: unknown) => { if (!abort.signal.aborted) setFacetState({ key: facetKey, error: error instanceof Error ? error.message : 'Counts unavailable' }) })
    return () => abort.abort()
  }, [api, kind, cohortKey, idsKey, facetKey, revision])
  const facets = facetState?.key.startsWith(`${kind}:${cohortKey}:`) ? facetState : null
  const rollups = facetState?.key === facetKey ? facetState.result?.task_rollups.scopes : undefined
  function changeQuery(next: ParentQuery) { navigationGeneration.current += 1; setPageStart(0); setChanged(false); setQuery(next) }
  function refresh() { navigationGeneration.current += 1; setPageStart(0); setChanged(false); void list.reload(); setRevision(n => n + 1) }
  async function nextPage() {
    const gen = navigationGeneration.current
    if (list.items.length <= pageStart + PARENT_PAGE_SIZE) {
      const before = fetchedPages.current
      await list.loadMore()
      if (gen !== navigationGeneration.current || fetchedPages.current === before) return
    }
    setPageStart(start => start + PARENT_PAGE_SIZE)
  }
  return { ...list, items, query, changeQuery, facets, rollups, refresh, live, pageStart, isStale: list.isStale || changed,
    previousPage: () => setPageStart(start => Math.max(0, start - PARENT_PAGE_SIZE)), nextPage,
    hasNext: list.items.length > pageStart + PARENT_PAGE_SIZE || list.hasMore }
}
