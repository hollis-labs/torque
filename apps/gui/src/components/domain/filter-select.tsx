import type { SelectHTMLAttributes } from 'react'
import { ChevronDown } from 'lucide-react'

/** Native keyboard behavior with the same inset arrow and sizing as dashboard filters. */
export function FilterSelect({ className = '', ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return <span className="relative inline-flex min-w-0 max-w-full">
    <select {...props} className={`h-8 w-full min-w-0 appearance-none rounded border border-zinc-800 bg-zinc-900/50 pl-3 pr-8 text-xs text-zinc-300 outline-none focus-visible:ring-2 focus-visible:ring-zinc-500 ${className}`} />
    <ChevronDown aria-hidden="true" className="pointer-events-none absolute right-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-zinc-500" />
  </span>
}
