// HITL domain types (H1). Modelled on the go-hitl wire (go-hitl is the one wire;
// Tangent is the human queue; the event bus only carries approval notifications)
// and Tangent's interaction kinds:
//   approval -> tangent.hitl-item          (operator-owned approval, typed evidence, immutable outcome)
//   question -> tangent.interview-question (quick-picks + long form) / tangent.feedback
//   form     -> tangent.form-collect       (schema-driven form)
//   secret   -> no Tangent kind yet (see BACKEND_NEEDS.md)
// Every string here originates from agents or remote systems: render as text only.
import type { ConsoleRef } from '../events/types'

export type HitlType = 'approval' | 'question' | 'secret' | 'form'
export const HITL_TYPES: readonly HitlType[] = ['approval', 'question', 'secret', 'form']

export type HitlRisk = 'low' | 'medium' | 'high' | 'critical'
export type HitlStatus = 'pending' | 'answered' | 'expired' | 'withdrawn'

export interface HitlRequester {
  id: string
  label?: string
  kind?: 'agent' | 'workflow' | 'service' | 'operator' | (string & {})
}

export type HitlAuditAction =
  | 'requested' | 'confirmed' | 'denied' | 'approved' | 'rejected' | 'deferred'
  | 'answered' | 'submitted' | 'expired' | 'withdrawn' | (string & {})

export interface HitlAuditEntry {
  at: number
  /** Who did it: a user id, an agent id or "system". */
  actor: string
  action: HitlAuditAction
  note?: string
}

export interface HitlOutcome {
  /** Short label shown on the card once closed, e.g. "Approved". */
  label: string
  at: number
  by?: string
}

interface HitlBase {
  id: string
  title: string
  prompt?: string
  requester: HitlRequester
  risk: HitlRisk
  createdAt: number
  /** Epoch ms. NOTE: Tangent does not enforce expires_at today, so the GUI also enforces it (advisory). */
  expiresAt?: number
  /** Where to look before deciding: tasks, PRs, agents, runs. */
  contextLinks: ConsoleRef[]
  status: HitlStatus
  audit: HitlAuditEntry[]
  outcome?: HitlOutcome
  correlationId?: string
  /** The Tangent interaction kind this maps to, when known. */
  tangentKind?: string
}

export interface HitlApprovalRequest extends HitlBase {
  type: 'approval'
  /** Permission scopes the approval grants; passed to IdentityConfirm. */
  scope: string[]
  /** What will happen if approved, one line each. */
  effects?: string[]
  allowDefer?: boolean
}

export interface HitlOption {
  value: string
  label: string
  description?: string
}

export interface HitlQuestionRequest extends HitlBase {
  type: 'question'
  /** choice: pick one; multi: pick any; text: free text. */
  mode: 'choice' | 'multi' | 'text'
  options: HitlOption[]
  allowOther?: boolean
  placeholder?: string
}

export interface HitlSecretRequest extends HitlBase {
  type: 'secret'
  fieldLabel: string
  /** Why the secret is needed. */
  purpose: string
  /** Where it will be delivered, e.g. "cerberus vault: github/token". */
  destination?: string
}

export type HitlFieldKind = 'text' | 'textarea' | 'number' | 'select' | 'checkbox'

export interface HitlField {
  id: string
  label: string
  kind: HitlFieldKind
  required?: boolean
  help?: string
  placeholder?: string
  options?: HitlOption[]
  default?: string | number | boolean
}

export interface HitlFormRequest extends HitlBase {
  type: 'form'
  fields: HitlField[]
  submitLabel?: string
}

export type HitlRequest = HitlApprovalRequest | HitlQuestionRequest | HitlSecretRequest | HitlFormRequest

export type HitlFormValues = Record<string, string | number | boolean>

/** Responses the client accepts. Secrets are NOT a response: they go through HitlClient.submitSecret. */
export type HitlResponse =
  | { type: 'approval'; decision: 'approve' | 'reject' | 'defer'; reason?: string }
  | { type: 'question'; answer: string | string[] }
  | { type: 'form'; values: HitlFormValues }

export const RISK_RANK: Record<HitlRisk, number> = { low: 0, medium: 1, high: 2, critical: 3 }
