// Workflow/steering domain model (H2). Plain serialisable data; every string
// can originate from an agent or remote system, so components render text only.
import type { ConsoleRef } from '../events/types'
import type { HitlAuditEntry } from '../hitl/types'

export type RunState = 'running' | 'paused' | 'blocked' | 'awaiting_plan' | 'completed' | 'failed' | 'cancelled'
export const ACTIVE_RUN_STATES: readonly RunState[] = ['running', 'paused', 'blocked', 'awaiting_plan']

export type RunPriority = 'low' | 'normal' | 'high' | 'urgent'
export const PRIORITIES: readonly RunPriority[] = ['low', 'normal', 'high', 'urgent']

export type PhaseStatus = 'pending' | 'active' | 'passed' | 'failed' | 'skipped'
export type GateStatus = 'open' | 'passed' | 'failed' | 'overridden'

export interface GateCriterion {
  label: string
  met: boolean
}

/** A gate sits at the end of a phase (mirrors the Hadron workflow gate/signal model; placeholder shape). */
export interface WorkflowGate {
  id: string
  label: string
  status: GateStatus
  criteria: GateCriterion[]
  /** A human must submit the gate (hadron_workflow_gate_submit); otherwise it passes itself when criteria are met. */
  requiresHuman: boolean
  evaluatedAt?: number
}

export interface WorkflowPhase {
  id: string
  label: string
  status: PhaseStatus
  /** Step-level progress inside the phase (progress.checkpoint). */
  progress?: { done: number; total: number }
  gate?: WorkflowGate
  startedAt?: number
  endedAt?: number
}

export interface PlanItem {
  id: string
  label: string
  summary?: string
  included: boolean
}

/** A plan the agent proposes; the human approves, edits (drops phases) or rejects it. */
export interface PlanProposal {
  status: 'pending' | 'approved' | 'rejected'
  items: PlanItem[]
  proposedAt: number
  note?: string
}

export interface CheckpointOption {
  value: string
  label: string
  description?: string
}

/** Mirrors a Torque task checkpoint (torque_task_checkpoint_emit/respond). */
export interface WorkflowCheckpoint {
  id: string
  question: string
  options: CheckpointOption[]
  allowText: boolean
  status: 'pending' | 'answered' | 'cancelled'
  answer?: string
  emittedAt: number
}

export interface WorkflowRun {
  id: string
  title: string
  state: RunState
  priority: RunPriority
  agent: { id: string; label?: string }
  phases: WorkflowPhase[]
  blockedReason?: string
  /** Latest redirect instruction applied to the agent. */
  redirect?: string
  escalatedTo?: string
  plan?: PlanProposal
  checkpoint?: WorkflowCheckpoint
  subjects: ConsoleRef[]
  audit: HitlAuditEntry[]
  createdAt: number
  updatedAt: number
}

export type SteerCommand =
  | { type: 'pause'; reason?: string }
  | { type: 'resume' }
  | { type: 'redirect'; instruction: string }
  | { type: 'escalate'; to: string; note?: string }
  | { type: 'priority'; priority: RunPriority }

export type SteerType = SteerCommand['type']
export const STEER_TYPES: readonly SteerType[] = ['pause', 'resume', 'redirect', 'escalate', 'priority']

export type GateDecision = 'pass' | 'fail' | 'override'
export type PlanDecision = 'approve' | 'reject'
