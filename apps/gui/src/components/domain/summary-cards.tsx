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
    <div className="grid grid-cols-4 gap-2 px-4 py-2">
      {cards.map((card) => (
        <div
          key={card.label}
          className="relative overflow-hidden rounded border border-zinc-800/80 bg-zinc-950 px-3 py-2"
        >
          <div className="text-[10px] uppercase tracking-[.16em] text-zinc-500">
            {card.label}
            {card.subtitle && (
              <span className="ml-2 normal-case tracking-normal text-zinc-600">{card.subtitle}</span>
            )}
          </div>
          <div className="mt-0.5">
            <span className="font-mono text-xl font-semibold text-zinc-100">
              {card.value}
            </span>
          </div>
          {card.accentColor && (
            <div
              className="absolute bottom-0 left-0 h-[2px] w-full"
              style={{ backgroundColor: card.accentColor }}
            />
          )}
        </div>
      ))}
    </div>
  )
}
