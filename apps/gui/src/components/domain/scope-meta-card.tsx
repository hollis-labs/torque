import type { ReactNode } from 'react'

interface ScopeMetaCardProps {
  label: string
  value: ReactNode
  className?: string
}

export function ScopeMetaCard({ label, value, className }: ScopeMetaCardProps) {
  return (
    <div className={`rounded-xl border border-zinc-800/80 bg-zinc-950/60 p-4 ${className ?? ''}`}>
      <div className="mb-2 text-[11px] uppercase tracking-[0.18em] text-zinc-500">{label}</div>
      <div className="whitespace-pre-wrap break-words text-sm text-zinc-200">{value}</div>
    </div>
  )
}
