import { useEffect, useState } from 'react'
import { Check, ChevronsUpDown } from 'lucide-react'
import { Button, Command, CommandGroup, CommandInput, CommandItem, CommandList, Popover, PopoverContent, PopoverTrigger } from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { useDebouncedCallback } from '@/hooks/use-debounced-callback'
import { usePagedList } from '@/hooks/use-paged-list'
import type { Epic, Project, Sprint } from '@/lib/types'

type Scope = Project | Epic | Sprint
export type ScopeKind = 'project' | 'epic' | 'sprint'
interface ScopePickerProps {
  kind: ScopeKind
  value?: string | null
  onChange: (id: string | null) => void
  projectId?: string
  label?: string
  placeholder?: string
  disabled?: boolean
  className?: string
}

/** One bounded server-search page; selected IDs are resolved independently. */
export function ScopePicker({ kind, value, onChange, projectId, label, placeholder, disabled, className }: ScopePickerProps) {
  const api = useApi()
  const [open, setOpen] = useState(false)
  const [input, setInput] = useState('')
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<Scope | null>(null)
  const [selectionError, setSelectionError] = useState(false)
  const scheduleSearch = useDebouncedCallback(() => setSearch(input.trim()), 250)
  const page = usePagedList<Scope, { search: string; project_id?: string }>({
    params: { search, project_id: projectId },
    queryKey: kind,
    enabled: open && search === input.trim(),
    getId: item => item.id,
    fetchPage: ({ params, signal }) => {
      const query = { ...params, limit: 50, sort_by: 'name', sort_dir: 'asc' as const }
      if (kind === 'project') return api.listProjects(undefined, query, signal)
      if (kind === 'epic') return api.listEpics(query, signal)
      return api.listSprints(query, signal)
    },
  })

  useEffect(() => {
    if (!value) return
    let active = true
    const request = kind === 'project' ? api.getProject(value) : kind === 'epic' ? api.getEpic(value) : api.getSprint(value)
    void request.then(item => { if (active) { setSelected(item); setSelectionError(false) } })
      .catch(() => { if (active) setSelectionError(true) })
    return () => { active = false }
  }, [api, kind, value])

  const selectedName = value ? (selected?.id === value ? selected.name : value) : placeholder ?? `No ${kind}`
  const needsSearch = !search && page.hasMore
  function changeOpen(next: boolean) {
    setOpen(next)
    if (next) { setInput(''); setSearch('') }
  }
  function choose(id: string | null) { onChange(id); setOpen(false) }

  return (
    <Popover open={open} onOpenChange={changeOpen}>
      <PopoverTrigger render={<Button variant="outline" disabled={disabled} role="combobox" aria-expanded={open} aria-label={label ?? `Choose ${kind}`} className={className ?? 'h-8 w-full justify-between font-normal'} />}>
        <span className="truncate">{selectedName}</span><ChevronsUpDown className="ml-2 size-3.5 shrink-0 opacity-50" />
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0" align="start">
        <Command shouldFilter={false}>
          <CommandInput placeholder={`Search ${kind}s…`} value={input} onValueChange={text => { setInput(text); scheduleSearch() }} aria-label={`Search ${kind}s`} />
          <CommandList>
            <CommandGroup>
              <CommandItem value="__none__" onSelect={() => choose(null)}><Check className={`size-4 ${value ? 'opacity-0' : ''}`} />{placeholder ?? `No ${kind}`}</CommandItem>
              {!needsSearch && page.items.map(item => (
                <CommandItem key={item.id} value={item.id} onSelect={() => choose(item.id)}><Check className={`size-4 ${value === item.id ? '' : 'opacity-0'}`} /><span className="truncate">{item.name}</span><span className="ml-auto text-xs text-muted-foreground">{item.id}</span></CommandItem>
              ))}
            </CommandGroup>
            <div className="px-3 py-2 text-xs text-muted-foreground" role="status">
              {page.loading || search !== input.trim() ? 'Searching…' : page.error ? 'Search failed. Close and reopen to retry.' : needsSearch ? `More than 50 ${kind}s match. Type to search.` : page.hasMore ? 'More matches available. Refine your search.' : page.items.length === 0 ? `No ${kind}s match your search.` : null}
              {selectionError && value && <p>Selected {kind} could not be loaded; its ID is retained.</p>}
            </div>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
