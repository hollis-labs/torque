import { useState, type ReactNode } from 'react'
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

export interface FilterEntityComboboxItem {
  id: string
  name: string
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
}: FilterEntityComboboxProps) {
  const [open, setOpen] = useState(false)
  const selected = value ? items.find((i) => i.id === value) : null
  const displayLabel = selected?.name ?? allLabel
  const isMuted = selected === null || selected === undefined

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
        <Command>
          <CommandInput placeholder="Search…" className="h-8 text-[11px]" />
          <CommandList>
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
              {items.map((item) => (
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
            {onCreate && (
              <>
                <CommandSeparator />
                <CommandGroup>
                  <CommandItem
                    value={createLabel ?? 'Create new'}
                    onSelect={() => {
                      onCreate()
                      setOpen(false)
                    }}
                  >
                    <Plus className="mr-1.5 h-3 w-3" />
                    {createLabel ?? 'Create new'}
                  </CommandItem>
                </CommandGroup>
              </>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
