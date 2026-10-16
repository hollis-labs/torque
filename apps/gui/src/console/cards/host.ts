// Host-surface controller: framework-free external store for the transient
// presentation state of cards (toasts, the modal, tray open/closed) plus
// manually presented cards that do not come from events (e.g. H1 HITL cards).
// Surfaces hold card IDS, never snapshots, so a card stays live while shown.
import type { CardInstance } from './types'

export interface ToastEntry {
  id: string
  cardId: string
  until: number
}

export interface CardHostState {
  toasts: ToastEntry[]
  modalCardId: string | null
  trayOpen: boolean
}

export interface CardHost {
  /** Show a card as a toast for `ms` (default 6000). Re-toasting the same card refreshes it. */
  toast(cardId: string, opts?: { ms?: number }): void
  dismissToast(id: string): void
  openModal(cardId: string): void
  closeModal(): void
  openTray(open?: boolean): void
  toggleTray(): void
  /** Present a card that did not come from an event rule; it joins useCards() until removed. Returns remove(). */
  present(card: CardInstance): () => void
  getState(): CardHostState
  getManual(): Record<string, CardInstance>
  subscribe(l: () => void): () => void
}

export function createCardHost(opts: { now?: () => number } = {}): CardHost {
  const now = opts.now ?? Date.now
  let state: CardHostState = { toasts: [], modalCardId: null, trayOpen: false }
  let manual: Record<string, CardInstance> = {}
  let seq = 0
  const ls = new Set<() => void>()
  const emit = () => ls.forEach((l) => l())
  const set = (patch: Partial<CardHostState>) => {
    state = { ...state, ...patch }
    emit()
  }
  return {
    toast(cardId, o) {
      const entry = { id: `t${++seq}`, cardId, until: now() + (o?.ms ?? 6000) }
      set({ toasts: [...state.toasts.filter((t) => t.cardId !== cardId), entry].slice(-3) })
    },
    dismissToast: (id) => set({ toasts: state.toasts.filter((t) => t.id !== id) }),
    openModal: (cardId) => set({ modalCardId: cardId }),
    closeModal: () => set({ modalCardId: null }),
    openTray: (open = true) => set({ trayOpen: open }),
    toggleTray: () => set({ trayOpen: !state.trayOpen }),
    present(card) {
      manual = { ...manual, [card.id]: card }
      emit()
      return () => {
        const { [card.id]: _drop, ...rest } = manual
        void _drop
        manual = rest
        emit()
      }
    },
    getState: () => state,
    getManual: () => manual,
    subscribe(l) {
      ls.add(l)
      return () => ls.delete(l)
    },
  }
}
