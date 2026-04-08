interface PriorityBadgeProps {
  priority: number
  className?: string
}

const PRIORITY_CONFIG: Record<number, { bg: string; text: string; label: string }> = {
  1: { bg: 'bg-rose-950/40',  text: 'text-rose-400',  label: 'P1' },
  2: { bg: 'bg-amber-950/40', text: 'text-amber-400', label: 'P2' },
  3: { bg: 'bg-zinc-900',     text: 'text-zinc-500',  label: 'P3' },
}

export function PriorityBadge({ priority, className = '' }: PriorityBadgeProps) {
  const config = PRIORITY_CONFIG[priority] ?? PRIORITY_CONFIG[3]

  return (
    <span
      className={`rounded px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-[.18em] ${config.bg} ${config.text} ${className}`}
    >
      {config.label}
    </span>
  )
}
