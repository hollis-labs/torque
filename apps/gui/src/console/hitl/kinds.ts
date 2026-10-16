import type { CardKindDef, CardRegistry } from '../cards/registry'
import { HitlApprovalCard, HitlFormCard, HitlQuestionCard, HitlSecretCard } from './cards'
import { HITL_CARD_KIND } from './model'

/** Card kinds for the K1 registry. High priority: HITL asks block an agent. */
export const HITL_KINDS: CardKindDef[] = [
  {
    id: HITL_CARD_KIND.approval,
    label: 'HITL approval',
    render: { expanded: HitlApprovalCard, compact: HitlApprovalCard, inline: HitlApprovalCard },
    actionSchema: [
      { id: 'approve', label: 'Approve', intent: 'primary', kind: 'hitl.approve' },
      { id: 'reject', label: 'Reject', intent: 'danger', kind: 'hitl.reject' },
    ],
    priority: 30,
  },
  { id: HITL_CARD_KIND.question, label: 'HITL question', render: { expanded: HitlQuestionCard, compact: HitlQuestionCard, inline: HitlQuestionCard }, priority: 25 },
  // Secrets never render inline in a page: the tray/modal only.
  { id: HITL_CARD_KIND.secret, label: 'HITL secret', render: { expanded: HitlSecretCard, compact: HitlSecretCard, inline: HitlSecretCard }, surfaces: ['tray', 'modal', 'toast'], priority: 28 },
  { id: HITL_CARD_KIND.form, label: 'HITL form', render: { expanded: HitlFormCard, compact: HitlFormCard, inline: HitlFormCard }, priority: 20 },
]

/** Registers all four kinds; returns one unregister. */
export function registerHitlKinds(registry: CardRegistry): () => void {
  const offs = HITL_KINDS.map((k) => registry.register(k))
  return () => offs.forEach((o) => o())
}
