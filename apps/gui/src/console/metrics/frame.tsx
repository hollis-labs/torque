import type { ReactNode } from 'react'

export const LABEL = 'font-mono text-[10px] uppercase tracking-widest text-zinc-500'

/** Shared loading / error / empty placeholders so every widget degrades identically. */
export function Placeholder({ kind, label, message, height = 48 }: { kind: 'loading' | 'error' | 'empty'; label: string; message?: string; height?: number }) {
  if (kind === 'loading') {
    return (
      <div role="status" aria-busy="true" aria-label={`${label} loading`} className="animate-pulse bg-zinc-900" style={{ height }} />
    )
  }
  return (
    <div
      role={kind === 'error' ? 'alert' : 'status'}
      className={`flex items-center justify-center border border-dashed border-zinc-800 px-3 font-mono text-xs ${kind === 'error' ? 'text-rose-300' : 'text-zinc-500'}`}
      style={{ height }}
    >
      {message ?? (kind === 'error' ? 'Unavailable' : 'No data')}
    </div>
  )
}

export function Legend({ children }: { children: ReactNode }) {
  return <ul className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-[11px] text-zinc-400">{children}</ul>
}
