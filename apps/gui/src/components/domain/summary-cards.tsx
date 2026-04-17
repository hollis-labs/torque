interface SummaryCard {
  label: string
  value: number | string
  subtitle?: string
  accentColor?: string
}

interface SummaryCardsProps {
  cards: SummaryCard[]
}

export function SummaryCards({ cards }: SummaryCardsProps) {
  return (
    <div className="flex items-center gap-x-5 gap-y-1 border-b border-zinc-800/80 bg-zinc-950 px-4 py-1.5 flex-wrap">
      {cards.map((card, i) => (
        <div key={card.label} className="flex items-center gap-2 text-[11px] leading-none">
          <span
            className="size-1.5 shrink-0 rounded-full"
            style={{ backgroundColor: card.accentColor ?? '#52525b' }}
            aria-hidden
          />
          <span className="uppercase tracking-[.16em] text-zinc-500">
            {card.label}
            {card.subtitle && (
              <span className="ml-1 normal-case tracking-normal text-zinc-600">{card.subtitle}</span>
            )}
          </span>
          <span className="font-mono text-[13px] font-semibold text-zinc-100 tabular-nums">
            {card.value}
          </span>
          {i < cards.length - 1 && (
            <span className="ml-3 h-3 w-px bg-zinc-800/80" aria-hidden />
          )}
        </div>
      ))}
    </div>
  )
}
