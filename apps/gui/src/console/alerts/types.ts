// Alert view model. Components render `Alert`, never raw ConsoleEvents, so the
// event -> alert mapping (mapping.ts) is the only place that knows event kinds
// and can be swapped (A3 rules, K1 card pipeline) without touching the UI.

import type { ConsoleSeverity } from '../events/types'
import { DEFAULT_CONSOLE_PREFS, type ConsolePrefs } from '../prefs/types'

export type AlertSeverity = ConsoleSeverity

/** Icon vocabulary; resolved to a glyph in AlertIcon so mappers stay UI-free. */
export type AlertIconKey = 'info' | 'task' | 'agent' | 'message' | 'approval' | 'pr' | 'warning' | 'error'

/** Phase 1: display only. A primary action is a link; A2/A3 add real actions through the ActionBus. */
export interface AlertLink {
  label: string
  /** Same-origin path ("/tasks/CW-1") or an absolute http(s) URL. Validated by safeHref. */
  href: string
}

export interface Alert {
  /** Event id: the key used for dismiss / snooze / read state. */
  id: string
  threadId?: string
  severity: AlertSeverity
  icon: AlertIconKey
  title: string
  /** One line. */
  detail?: string
  /** Agent / machine / producer shown as the source. */
  source: string
  ts: number
  kind: string
  primary?: AlertLink
  /** Set by alert rules (A3): alerts sharing a key inside the coalesce window fold into one entry. */
  coalesceKey?: string
  /** Per-rule coalesce window override, ms. */
  coalesceWindowMs?: number
  /** How many events folded into this alert (>= 2 only when coalesced). */
  count?: number
  /** Rule-declared escalation: an unread alert older than `afterMs` is shown at severity `to`. */
  escalate?: { afterMs: number; to: AlertSeverity }
  /** Id of the rule that produced this alert (diagnostics). */
  ruleId?: string
}

export type AlertMapper = (event: import('../events/types').ConsoleEvent) => Alert | null

/** The alert-relevant user preferences; the same object as ConsolePrefs (see ../prefs). */
export type AlertPrefs = ConsolePrefs

export const DEFAULT_ALERT_PREFS: AlertPrefs = DEFAULT_CONSOLE_PREFS
