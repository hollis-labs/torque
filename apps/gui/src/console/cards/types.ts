// Card pipeline types (K1). A CardInstance is derived from ConsoleEvents by
// data-driven rules (rules.ts); it is never stored. Everything here is plain
// serialisable data so rules can later be loaded from config or the server.
import type { ConsoleActionOffer, ConsoleEvent, ConsoleRef, ConsoleSeverity } from '../events/types'
import type { ConsoleAction } from '../seams/action-bus'

/** Open string: registered in the CardRegistry. Reference kinds: "info", "approval", "status". */
export type CardKindId = string

export type CardSurface = 'toast' | 'tray' | 'modal' | 'inline'
export type CardDensity = 'compact' | 'expanded' | 'inline'

/** live: needs attention or reflects current state. resolved: closed by a later event or by a user action. superseded/expired: no longer current. */
export type CardPhase = 'live' | 'resolved' | 'superseded' | 'expired'

export type ActionIntent = 'primary' | 'neutral' | 'danger'

/** Declarative action. `kind` is the ActionBus handler ref. Intrinsic kinds "dismiss" and "snooze" are handled by the StateStore, not the bus. */
export interface CardActionSpec {
  id: string
  label: string
  intent: ActionIntent
  kind: string
  params?: Record<string, unknown>
  /** Present => the ActionBus runs IdentityConfirm first. */
  confirm?: NonNullable<ConsoleAction['confirm']>
  /** On an "ok" result, settle the card with this resolution (e.g. "approved"). */
  settlesAs?: string
  /** Where the action came from; offered actions mirror the event. */
  origin?: 'offered' | 'declared'
}

export interface CardInstance {
  /** Stable per dedupe key: the StateStore key and the React key. */
  id: string
  kind: CardKindId
  ruleId: string
  title: string
  summary?: string
  severity: ConsoleSeverity
  phase: CardPhase
  /** Epoch ms of the first / latest contributing event. */
  firstTs: number
  ts: number
  /** Number of events coalesced into this card. */
  count: number
  surfaces: CardSurface[]
  subjects: ConsoleRef[]
  /** Latest contributing event: components read kind-specific payload from here (untrusted). */
  event: ConsoleEvent
  actions: CardActionSpec[]
  /** Epoch ms after which the card is expired (phase "expired"), if a ttl applies. */
  expiresAt?: number
  /** Set when phase is "resolved": how and by which event. */
  resolution?: { label: string; eventId?: string; ts: number }
  /** Free fields a rule can pass through to the component. */
  props: Record<string, unknown>
}

export interface CardActionController {
  /** Run one of the card's actions (confirm, bus dispatch, settle). */
  run(action: CardActionSpec): Promise<void>
  /** Per-action in-flight / last-result state. */
  status: Record<string, { pending: boolean; ok?: boolean; message?: string }>
  /** Dismiss / snooze straight through the StateStore. */
  dismiss(): Promise<void>
  snooze(ms: number): Promise<void>
}

export interface CardRenderProps {
  card: CardInstance
  density: CardDensity
  controller: CardActionController
  /** Optional: host asks to open this card in a larger surface. */
  onExpand?: () => void
}

export type { ConsoleActionOffer }
