// Alert rules are DATA (JSON-serialisable) so agents and automations can contribute
// packs later. The engine (engine.ts) is the only code that interprets them.
// Event kinds are placeholders until the data team's catalog lands: keep kinds in
// rules, never in code, and prefer matching on class.category / labels / severity.

import type { ConsoleSeverity } from '../events/types'
import type { AlertIconKey } from '../alerts/types'

/** A payload condition: a bare scalar means equality. */
export type PayloadCondition = string | number | boolean | { eq?: string | number | boolean; in?: Array<string | number | boolean>; gte?: number; lte?: number; exists?: boolean }

export interface AlertRuleMatch {
  /** Exact kinds, or a prefix ending in ".*" (e.g. "pr.*"). Omitted = any kind. */
  kinds?: string[]
  /** Event source ids (source.id). */
  sources?: string[]
  /** Event severity at or above this. */
  minSeverity?: ConsoleSeverity
  /** Event severity is one of these. */
  severities?: ConsoleSeverity[]
  /** Envelope v1.1 class.category is one of these. */
  categories?: string[]
  /** Envelope v1.1 class.labels contains any of these. */
  labels?: string[]
  /** Event has a subject of any of these types. */
  subjectTypes?: string[]
  /** Dotted payload path -> condition. All must hold. */
  payload?: Record<string, PayloadCondition>
}

export interface RuleAlert {
  /** 'inherit' (default) keeps the event's severity. */
  severity?: ConsoleSeverity | 'inherit'
  icon?: AlertIconKey
  /** Template: {{payload.x}}, {{subject.label}}, {{subject.id}}, {{agent}}, {{source}}, {{kind}}, {{severity}}; "{{x|fallback}}". */
  title: string
  detail?: string
}

export interface RuleCoalesce {
  /** Template for the coalesce key. Same key within the window folds into one entry. */
  key: string
  /** Overrides the user's window (ms resolution is derived from seconds). */
  windowSec?: number
}

export interface AlertRule {
  id: string
  description?: string
  /** false disables the rule without removing it. */
  enabled?: boolean
  match: AlertRuleMatch
  /** The first enabled matching rule wins. 'drop' makes the event not an alert. */
  action?: 'alert' | 'drop'
  alert?: RuleAlert
  coalesce?: RuleCoalesce
  /** Unread longer than afterSec: show at severity `to`. */
  escalate?: { afterSec: number; to: ConsoleSeverity }
}

export interface AlertRulePack {
  /** Pack format version. */
  version: 1
  id: string
  label?: string
  rules: AlertRule[]
}
