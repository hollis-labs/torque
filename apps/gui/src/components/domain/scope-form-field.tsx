import type { ReactNode } from 'react'

interface ScopeFormFieldProps {
  label: string
  children: ReactNode
  className?: string
}

export function ScopeFormField({ label, children, className }: ScopeFormFieldProps) {
  return (
    <div className={className}>
      <div className="mb-1 text-[11px] uppercase tracking-[0.18em] text-zinc-500">{label}</div>
      {children}
    </div>
  )
}
