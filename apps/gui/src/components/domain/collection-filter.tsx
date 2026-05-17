import { useState } from 'react'
import { Inbox, Search, X, SlidersHorizontal } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FilterCycleToggle, type CycleOption } from './filter-bar/filter-cycle-toggle'
import { STATUS_COLORS, DEFAULT_STATUS_COLOR, TASK_STATUSES } from '@/lib/constants'
import type { TaskStatus } from '@/lib/types'

const INBOX_CYCLE_OPTIONS: readonly [
  CycleOption<boolean>,
  ...CycleOption<boolean>[],
] = [
  { value: false, label: 'Hide Inbox', dotColor: 'bg-zinc-500', title: 'Hide inbox tasks' },
  { value: true, label: 'Show Inbox', dotColor: 'bg-green-500', title: 'Show inbox tasks' },
]

interface CollectionFilterProps {
  searchQuery: string
  onSearchChange: (query: string) => void
  showInbox: boolean
  onInboxToggle: (show: boolean) => void
  selectedStatuses?: TaskStatus[]
  onStatusToggle?: (status: TaskStatus) => void
  onClearFilters?: () => void
}

export function CollectionFilter({
  searchQuery,
  onSearchChange,
  showInbox,
  onInboxToggle,
  selectedStatuses = [],
  onStatusToggle,
  onClearFilters,
}: CollectionFilterProps) {
  const [localQuery, setLocalQuery] = useState(searchQuery)
  const hasActiveFilters = searchQuery.length > 0 || selectedStatuses.length < TASK_STATUSES.length

  function handleSearchSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSearchChange(localQuery.trim())
  }

  function handleClearSearch() {
    setLocalQuery('')
    onSearchChange('')
  }

  return (
    <div className="bg-zinc-950/80 border border-zinc-800/60 rounded-lg p-4 space-y-4">
      {/* Top Row: Search and Controls */}
      <div className="flex items-center justify-between gap-4">
        {/* Search */}
        <form onSubmit={handleSearchSubmit} className="flex-1 max-w-md">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 transform -translate-y-1/2 h-4 w-4 text-zinc-400" />
            <Input
              value={localQuery}
              onChange={(e) => setLocalQuery(e.target.value)}
              placeholder="Search collections and tasks..."
              className="pl-10 pr-10 bg-zinc-900/50 border-zinc-700 focus:border-zinc-600"
            />
            {localQuery && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={handleClearSearch}
                className="absolute right-1 top-1/2 transform -translate-y-1/2 h-6 w-6 p-0 text-zinc-400 hover:text-zinc-300"
              >
                <X className="h-3 w-3" />
              </Button>
            )}
          </div>
        </form>

        {/* Controls */}
        <div className="flex items-center gap-3">
          {/* Inbox Toggle */}
          <FilterCycleToggle
            options={INBOX_CYCLE_OPTIONS}
            value={showInbox}
            onValueChange={onInboxToggle}
            icon={Inbox}
          />

          {/* Clear Filters */}
          {hasActiveFilters && onClearFilters && (
            <Button 
              variant="outline" 
              size="sm"
              onClick={onClearFilters}
              className="text-zinc-400 border-zinc-700 hover:border-zinc-600 hover:bg-zinc-800"
            >
              <SlidersHorizontal className="h-4 w-4 mr-2" />
              Clear
            </Button>
          )}
        </div>
      </div>

      {/* Status Filter Chips */}
      {onStatusToggle && (
        <div className="space-y-2">
          <div className="flex items-center gap-2 text-sm text-zinc-400">
            <SlidersHorizontal className="h-4 w-4" />
            Status Filters
          </div>
          <div className="flex flex-wrap gap-2">
            {TASK_STATUSES.map(status => {
              const isActive = selectedStatuses.includes(status)
              const statusColor = STATUS_COLORS[status] || DEFAULT_STATUS_COLOR
              
              return (
                <button
                  key={status}
                  onClick={() => onStatusToggle(status)}
                  className={`
                    inline-flex items-center gap-2 px-3 py-1.5 rounded-md text-sm font-medium
                    transition-all duration-200 border
                    ${isActive
                      ? 'bg-blue-500/20 text-blue-400 border-blue-500/40 shadow-sm'
                      : 'bg-zinc-800/40 text-zinc-400 border-zinc-700/60 hover:bg-zinc-700/40 hover:text-zinc-300'
                    }
                  `}
                >
                  <div 
                    className={`w-2 h-2 rounded-full ${
                      isActive ? 'bg-blue-400' : 'bg-zinc-500'
                    }`}
                  />
                  {status.replace('_', ' ').replace(/\b\w/g, l => l.toUpperCase())}
                </button>
              )
            })}
          </div>
        </div>
      )}

      {/* Active Filter Summary */}
      {hasActiveFilters && (
        <div className="text-xs text-zinc-500 pt-2 border-t border-zinc-800/60">
          Showing{' '}
          {selectedStatuses.length < TASK_STATUSES.length && (
            <>
              {selectedStatuses.length} of {TASK_STATUSES.length} statuses
              {searchQuery && ', '}
            </>
          )}
          {searchQuery && (
            <>matching "{searchQuery}"</>
          )}
          {!showInbox && (
            <>, excluding inbox tasks</>
          )}
        </div>
      )}
    </div>
  )
}