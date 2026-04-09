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
    <div className="grid grid-cols-4 gap-3 px-4 py-3">
      {cards.map((card) => (
        <div
          key={card.label}
          className="rounded-md border border-zinc-800/80 bg-zinc-950 p-3"
        >
          <div className="text-[10px] uppercase tracking-[.16em] text-zinc-500">
            {card.label}
          </div>
          <div className="mt-1 flex items-baseline gap-2">
            <span className="font-mono text-2xl font-semibold text-zinc-100">
              {card.value}
            </span>
            {card.subtitle && (
              <span className="text-[10px] text-zinc-500">{card.subtitle}</span>
            )}
          </div>
          {card.accentColor && (
            <div
              className="mt-2 h-0.5 w-full rounded-full"
              style={{ backgroundColor: card.accentColor }}
            />
          )}
        </div>
      ))}
    </div>
  )
}
