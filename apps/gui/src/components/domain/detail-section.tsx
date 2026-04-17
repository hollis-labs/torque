import { useState, type ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'

export type DetailSectionAccent = 'blue' | 'violet' | 'amber' | 'red' | 'zinc' | 'teal'

const ACCENT_CLASSES: Record<DetailSectionAccent, string> = {
  blue: 'border-l-blue-500/70',
  violet: 'border-l-violet-500/70',
  amber: 'border-l-amber-500/70',
  red: 'border-l-red-500/70',
  zinc: 'border-l-zinc-500/50',
  teal: 'border-l-teal-500/70',
}

interface DetailSectionProps {
  label: string
  accent: DetailSectionAccent
  children: ReactNode
  /** Default true. Set to false for the always-open Properties section. */
  collapsible?: boolean
  /** Only meaningful when collapsible is true. Default false. */
  defaultOpen?: boolean
  /** Inline summary shown next to the label when collapsed. */
  summary?: string
  /** Tailwind classes appended to the root element. */
  className?: string
}

export function DetailSection({
  label,
  accent,
  children,
  collapsible = true,
  defaultOpen = false,
  summary,
  className,
}: DetailSectionProps) {
  const [open, setOpen] = useState(collapsible ? defaultOpen : true)
  const showChildren = !collapsible || open

  return (
    <section
      className={cn(
        'rounded-md border border-zinc-800/50 border-l-2 bg-zinc-950',
        ACCENT_CLASSES[accent],
        className,
      )}
    >
      {collapsible ? (
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left hover:bg-zinc-900/40 transition-colors"
          aria-expanded={open}
        >
          <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
            {label}
          </span>
          <span className="flex items-center gap-2 text-[10px] text-zinc-600">
            {summary && !open && <span className="truncate max-w-md">{summary}</span>}
            <ChevronRight
              className={cn(
                'h-3 w-3 transition-transform',
                open && 'rotate-90',
              )}
            />
          </span>
        </button>
      ) : (
        <div className="px-3 py-2">
          <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
            {label}
          </span>
        </div>
      )}

      {showChildren && (
        <div className="border-t border-zinc-800/50 px-3 py-3">
          {children}
        </div>
      )}
    </section>
  )
}
