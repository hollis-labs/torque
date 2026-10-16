// Host surfaces. Consumers (A1 alert tray, H1 HITL, inline slots) use:
//   <CardTray/>        list surface (the tray body; the shell decides where it lives)
//   <CardInlineSlot/>  cards for a subject, embedded in a page
//   useCardHost()      toast(cardId) / openModal(cardId) / openTray()
//   <CardToaster/>, <CardModal/>  mounted by CardRuntimeProvider unless mountSurfaces={false}
import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { useConsoleState } from '../events/hooks'
import { createCardHost } from './host'
import { createDefaultCardRegistry } from './kinds'
import { DEFAULT_CARD_RULES } from './rules'
import { RuntimeContext, useCard, useCardController, useCardHost, useCardRegistry, useCards, type CardRuntime, type CardRuntimeProviderProps, type UseCardsOptions } from './runtime'
import type { CardDensity, CardInstance } from './types'

/** Renders any card at any density through the registry. */
export function CardView({ card, density = 'expanded', onExpand }: { card: CardInstance; density?: CardDensity; onExpand?: () => void }) {
  const registry = useCardRegistry()
  const controller = useCardController(card)
  return registry.render({ card, density, controller, onExpand })
}

function useHostState() {
  const host = useCardHost()
  return useSyncExternalStore(host.subscribe, host.getState, host.getState)
}

export interface CardTrayProps extends Omit<UseCardsOptions, 'surface'> {
  title?: string
  /** "inline" (default) expands a row in place; "modal" opens the host modal. */
  expandIn?: 'inline' | 'modal'
  emptyText?: string
}

export function CardTray({ title = 'Alerts', expandIn = 'inline', emptyText = 'Nothing needs attention.', ...opts }: CardTrayProps) {
  const cards = useCards({ ...opts, surface: 'tray' })
  const host = useCardHost()
  const [open, setOpen] = useState<string | null>(null)
  return (
    <section aria-label={title} className="flex flex-col border-t border-zinc-800 bg-zinc-950">
      <header className="flex items-center gap-2 border-b border-zinc-800 px-4 py-2">
        <h2 className="font-mono text-[10px] uppercase tracking-[0.2em] text-zinc-400">{title}</h2>
        <span className="font-mono text-[10px] text-zinc-500">{cards.length}</span>
      </header>
      {cards.length === 0 ? (
        <p className="px-4 py-6 text-center font-mono text-xs text-zinc-500">{emptyText}</p>
      ) : (
        <ul>
          {cards.map((c) => (
            <li key={c.id}>
              <CardView
                card={c}
                density={open === c.id ? 'expanded' : 'compact'}
                onExpand={() => (expandIn === 'modal' ? host.openModal(c.id) : setOpen(open === c.id ? null : c.id))}
              />
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

export interface CardInlineSlotProps extends Omit<UseCardsOptions, 'surface'> {
  density?: CardDensity
  /** Render nothing when there are no cards (default true). */
  hideWhenEmpty?: boolean
}

export function CardInlineSlot({ density = 'compact', hideWhenEmpty = true, ...opts }: CardInlineSlotProps) {
  const cards = useCards({ ...opts, surface: 'inline' })
  if (cards.length === 0 && hideWhenEmpty) return null
  return (
    <div data-card-slot="inline" className="flex flex-col">
      {cards.map((c) => (
        <CardView key={c.id} card={c} density={density} />
      ))}
    </div>
  )
}

export function CardToaster({ ms = 6000 }: { ms?: number }) {
  const host = useCardHost()
  const { toasts } = useHostState()
  const all = useCards({ surface: 'toast', includeClosed: false })
  const { ready } = useConsoleState()
  const seen = useRef<Set<string> | null>(null)
  // Auto-toast only cards that appear after the feed is ready (never the backlog).
  useEffect(() => {
    if (!ready) return
    if (seen.current === null) {
      seen.current = new Set(all.map((c) => `${c.id}@${c.ts}`))
      return
    }
    for (const c of all) {
      const k = `${c.id}@${c.ts}`
      if (!seen.current.has(k)) {
        seen.current.add(k)
        host.toast(c.id, { ms })
      }
    }
  }, [all, ready, host, ms])
  useEffect(() => {
    if (toasts.length === 0) return
    const next = Math.min(...toasts.map((t) => t.until))
    const t = setTimeout(() => toasts.filter((x) => x.until <= Date.now()).forEach((x) => host.dismissToast(x.id)), Math.max(50, next - Date.now()))
    return () => clearTimeout(t)
  }, [toasts, host])
  if (toasts.length === 0) return null
  return (
    <div aria-live="polite" className="pointer-events-none fixed bottom-4 right-4 z-50 flex w-80 flex-col gap-0">
      {toasts.map((t) => (
        <ToastItem key={t.id} id={t.id} cardId={t.cardId} />
      ))}
    </div>
  )
}

function ToastItem({ id, cardId }: { id: string; cardId: string }) {
  const host = useCardHost()
  const card = useCard(cardId)
  if (!card) return null
  return (
    <div role="status" className="pointer-events-auto border border-zinc-700 bg-zinc-950">
      <CardView card={card} density="compact" onExpand={() => { host.dismissToast(id); host.openModal(card.id) }} />
      <button type="button" aria-label="Close notification" onClick={() => host.dismissToast(id)} className="absolute right-1 top-1 px-1.5 font-mono text-xs text-zinc-500 hover:text-zinc-100">x</button>
    </div>
  )
}

export function CardModal() {
  const host = useCardHost()
  const { modalCardId } = useHostState()
  const card = useCard(modalCardId)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!modalCardId) return
    ref.current?.focus()
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && host.closeModal()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [modalCardId, host])
  if (!modalCardId || !card) return null
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/60 pt-[12vh]" onMouseDown={(e) => e.target === e.currentTarget && host.closeModal()}>
      <div ref={ref} role="dialog" aria-modal="true" aria-label={card.title} tabIndex={-1} className="w-full max-w-lg border border-zinc-700 bg-zinc-950 outline-none">
        <CardView card={card} density="expanded" />
        <div className="flex justify-end border-t border-zinc-800 px-3 py-1.5">
          <button type="button" onClick={() => host.closeModal()} className="px-2 py-1 font-mono text-[11px] uppercase tracking-wider text-zinc-400 hover:text-zinc-100">Close</button>
        </div>
      </div>
    </div>
  )
}

/** Mount once under ConsoleProvider. */
export function CardRuntimeProvider(props: CardRuntimeProviderProps) {
  const [runtime] = useState<CardRuntime>(() => ({
    registry: props.registry ?? createDefaultCardRegistry(),
    rules: props.rules ?? DEFAULT_CARD_RULES,
    host: props.host ?? createCardHost(),
  }))
  return (
    <RuntimeContext.Provider value={runtime}>
      {props.children}
      {props.mountSurfaces === false ? null : (
        <>
          <CardToaster />
          <CardModal />
        </>
      )}
    </RuntimeContext.Provider>
  )
}
