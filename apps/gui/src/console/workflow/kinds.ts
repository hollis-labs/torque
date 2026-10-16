import type { CardKindDef, CardRegistry } from '../cards/registry'
import { WorkflowCheckpointCard, WorkflowDecisionCard, WorkflowGateCard, WorkflowPlanCard, WorkflowProgressCard, WorkflowSteerCard } from './cards'
import { WORKFLOW_CARD_KIND as K } from './model'

const all = (c: CardKindDef['render']['expanded']) => ({ expanded: c, compact: c, inline: c })

/** Card kinds for the K1 registry. Plan/gate/checkpoint block an agent, so they outrank progress and steering. */
export const WORKFLOW_KINDS: CardKindDef[] = [
  { id: K.plan, label: 'Plan approval', render: all(WorkflowPlanCard), priority: 29 },
  { id: K.gate, label: 'Workflow gate', render: all(WorkflowGateCard), priority: 27 },
  { id: K.checkpoint, label: 'Checkpoint response', render: all(WorkflowCheckpointCard), priority: 26 },
  { id: K.decision, label: 'Flow decision', render: all(WorkflowDecisionCard), priority: 24 },
  { id: K.steer, label: 'Steer agent', render: all(WorkflowSteerCard), surfaces: ['tray', 'modal', 'inline'], priority: 12 },
  { id: K.progress, label: 'Workflow progress', render: all(WorkflowProgressCard), surfaces: ['tray', 'modal', 'inline'], priority: 5 },
]

/** Registers all workflow kinds; returns one unregister. */
export function registerWorkflowKinds(registry: CardRegistry): () => void {
  const offs = WORKFLOW_KINDS.map((k) => registry.register(k))
  return () => offs.forEach((o) => o())
}
