import { useMemo, useState, type ReactNode } from 'react'
import { ChevronDown, Plus } from 'lucide-react'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { ScrollArea } from '@/components/ui/scroll-area'

export interface FilterEntityComboboxItem {
  id: string
  name: string
  status?: string | null
  updated_at?: string | null
}

interface FilterEntityComboboxProps {
  icon: ReactNode
  items: FilterEntityComboboxItem[]
  value: string | null
  onChange: (id: string | null) => void
  allLabel: string
  ariaLabel: string
  onCreate?: () => void
  createLabel?: string
  showStateControls?: boolean
}

export function FilterEntityCombobox({
  icon,
  items,
  value,
  onChange,
  allLabel,
  ariaLabel,
  onCreate,
  createLabel,
  showStateControls = false,
}: FilterEntityComboboxProps) {
  const [open, setOpen] = useState(false)
  const [showInactive, setShowInactive] = useState(false)
  const [sortOrder, setSortOrder] = useState<'updated' | 'title'>('updated')
  const selected = value ? items.find((i) => i.id === value) : null
  const displayLabel = selected?.name ?? allLabel
  const isMuted = selected === null || selected === undefined
  const commandHeight = useMemo(() => (onCreate ? 'h-[300px]' : ''), [onCreate])
  const visibleItems = useMemo(() => {
    const filtered = showStateControls && !showInactive
      ? items.filter((item) => !item.status || item.status === 'active')
      : items
    return [...filtered].sort((a, b) => {
      if (sortOrder === 'title') return a.name.localeCompare(b.name)
      return (b.updated_at ?? '').localeCompare(a.updated_at ?? '')
    })
  }, [items, showInactive, showStateControls, sortOrder])

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        aria-label={ariaLabel}
        className="inline-flex h-7 items-center gap-1.5 rounded border border-zinc-800 bg-zinc-900/50 px-2 text-[10px] tracking-wider transition-colors hover:border-zinc-600"
      >
        <span className="text-zinc-400">{icon}</span>
        <span className={isMuted ? 'text-zinc-500' : 'text-zinc-100'}>{displayLabel}</span>
        <ChevronDown className="h-3 w-3 text-zinc-600" />
      </PopoverTrigger>
      <PopoverContent className="w-56 p-0" align="start">
        <Command className={commandHeight}>
          <CommandInput placeholder="Search…" className="h-8 text-[11px]" />
          {showStateControls && (
            <div className="flex items-center gap-2 border-b border-zinc-800/80 px-2 py-2 text-[11px]">
              <label className="flex items-center gap-1.5 text-zinc-400">
                <input type="checkbox" checked={showInactive} onChange={(e) => setShowInactive(e.target.checked)} />
                Show inactive
              </label>
              <select
                className="ml-auto h-7 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-[11px] text-zinc-200"
                value={sortOrder}
                onChange={(e) => setSortOrder(e.target.value as 'updated' | 'title')}
              >
                <option value="updated">Last updated</option>
                <option value="title">Title</option>
              </select>
            </div>
          )}
          <ScrollArea className="min-h-0 flex-1">
            <CommandList className="max-h-none">
              <CommandEmpty>No results.</CommandEmpty>
              <CommandGroup>
                <CommandItem
                  value={allLabel}
                  onSelect={() => {
                    onChange(null)
                    setOpen(false)
                  }}
                >
                  <span className="text-zinc-400">{allLabel}</span>
                </CommandItem>
                {visibleItems.map((item) => (
                  <CommandItem
                    key={item.id}
                    value={item.name}
                    onSelect={() => {
                      onChange(item.id)
                      setOpen(false)
                    }}
                  >
                    {item.name}
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </ScrollArea>
          {onCreate && (
            <>
              <CommandSeparator />
              <div className="sticky bottom-0 border-t border-zinc-800/80 bg-popover p-1">
                <button
                  type="button"
                  className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm text-zinc-100 transition-colors hover:bg-muted"
                  onClick={() => {
                    onCreate()
                    setOpen(false)
                  }}
                >
                  <Plus className="h-3 w-3" />
                  {createLabel ?? 'Create new'}
                </button>
              </div>
            </>
          )}
        </Command>
      </PopoverContent>
    </Popover>
  )
}
