interface ProgressBarProps {
  value: number // 0-100
  className?: string
}

export function ProgressBar({ value, className = '' }: ProgressBarProps) {
  const clamped = Math.max(0, Math.min(100, value))
  return (
    <div className={`h-1 w-full rounded-full bg-zinc-800 ${className}`}>
      <div
        className="h-full rounded-full bg-emerald-500 transition-all duration-300"
        style={{ width: `${clamped}%` }}
      />
    </div>
  )
}
