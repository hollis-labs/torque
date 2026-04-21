export interface CycleOption<T extends string> {
  value: T
  label: string
  /** Tailwind bg- class for the leading dot; omit for no dot. */
  dotColor?: string
  /** title attribute for the button (tooltip). */
  title?: string
}

interface FilterCycleToggleProps<T extends string> {
  /**
   * Non-empty list of options. The type enforces at least one element so the
   * component never has to handle an empty array at runtime (no NaN indexes,
   * no undefined `active`/`next`). Callers with a constant list should pass a
   * tuple literal; dynamic sources should assert non-empty before rendering.
   */
  options: readonly [CycleOption<T>, ...CycleOption<T>[]]
  value: T
  onChange: (next: T) => void
  ariaLabel: string
}

export function FilterCycleToggle<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
}: FilterCycleToggleProps<T>) {
  const idx = options.findIndex((o) => o.value === value)
  // If the current value isn't found, treat it as "before the first option" so
  // the next click lands on index 0's successor. This keeps corrupted state
  // recoverable via a single click instead of trapping the user.
  const active = idx >= 0 ? options[idx] : options[0]
  const nextIdx = idx >= 0 ? (idx + 1) % options.length : 1 % options.length
  const next = options[nextIdx]

  return (
    <button
      type="button"
      aria-label={`${ariaLabel}, current: ${active.label}`}
      title={active.title ?? `${ariaLabel}: ${active.label}`}
      onClick={() => onChange(next.value)}
      className="inline-flex items-center gap-1.5 rounded border border-zinc-800 bg-zinc-900/50 px-2 py-0.5 text-[10px] uppercase tracking-wider text-zinc-200 transition-all hover:border-zinc-600"
    >
      {active.dotColor && (
        <span
          data-testid="cycle-dot"
          className={`inline-block h-1.5 w-1.5 rounded-full ${active.dotColor}`}
        />
      )}
      <span>{active.label}</span>
    </button>
  )
}
