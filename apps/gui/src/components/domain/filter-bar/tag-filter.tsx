import { useState } from 'react'
import { Hash, Plus } from 'lucide-react'
import { Popover, PopoverTrigger, PopoverContent } from '@hollis-labs/sysop-ui'
import type { Tag, TagColor } from '@/lib/types'

const colors: TagColor[] = ['zinc', 'red', 'orange', 'amber', 'green', 'teal', 'blue', 'violet', 'pink']

export function TagFilter({ tags, selected, value, onChange, query, onQueryChange, color, onColorChange, onMore, loading, error, onRetry, onCreate }: {
  tags: Tag[]; selected?: Tag | null; value: string | null; onChange: (value: string | null) => void
  query: string; onQueryChange: (query: string) => void; color: TagColor | ''; onColorChange: (color: TagColor | '') => void
  onMore?: () => void; loading?: boolean; error?: string; onRetry?: () => void; onCreate?: () => void
}) {
  const [open, setOpen] = useState(false)
  const select = (slug: string | null) => { onChange(slug); setOpen(false) }
  return <Popover open={open} onOpenChange={setOpen}>
    <PopoverTrigger aria-label="Filter by tag"
      className="inline-flex h-7 items-center gap-1.5 rounded border border-border-subtle bg-panel-2/50 px-2 text-caption text-text-soft">
      <Hash className="h-3 w-3" />{selected?.name ?? tags.find(tag => tag.slug === value)?.name ?? value ?? 'All Tags'}
    </PopoverTrigger>
    <PopoverContent align="start" className="flex w-64 flex-col gap-2 rounded border border-border-subtle bg-panel p-2 shadow-lg">
      <input aria-label="Search tags" placeholder="Search tags…" value={query} onChange={event => onQueryChange(event.target.value)}
        className="h-8 rounded border border-border-subtle bg-panel-2 px-2 text-sm" />
      <select aria-label="Tag color" value={color} onChange={event => onColorChange(event.target.value as TagColor | '')}
        className="h-8 rounded border border-border-subtle bg-panel-2 px-2 text-sm">
        <option value="">All colors</option>{colors.map(value => <option key={value} value={value}>{value}</option>)}
      </select>
      {error && <p role="alert" className="text-xs">{error} <button onClick={onRetry}>Retry</button></p>}
      {loading && <p role="status" className="text-xs">Loading tags…</p>}
      <div className="flex max-h-60 flex-col overflow-y-auto">
        <button type="button" onClick={() => select(null)} className="px-2 py-1 text-left text-sm hover:bg-panel-2">All Tags</button>
        {tags.map(tag => <button type="button" key={tag.slug} onClick={() => select(tag.slug)}
          className="px-2 py-1 text-left text-sm hover:bg-panel-2">{tag.name}</button>)}
        {!loading && !tags.length && <p className="px-2 text-xs text-text-soft">No tags found.</p>}
      </div>
      {onMore && <button type="button" disabled={loading} onClick={onMore} className="text-xs">Load more tags</button>}
      {onCreate && <button type="button" onClick={() => { setOpen(false); onCreate() }} className="inline-flex items-center gap-1 text-xs"><Plus className="h-3 w-3" />New Tag</button>}
    </PopoverContent>
  </Popover>
}
