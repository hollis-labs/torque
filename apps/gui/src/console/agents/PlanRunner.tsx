import { useState } from 'react'
import { AGENT_ACTIONS, agentPlanAction, type AgentActionKind } from './actions'
import { useAgentEdit } from './edit-context'
import type { AgentPlan, ApplyOutcome } from './plan'
import { PlanReview } from './PlanReview'
import { StepUpHost } from './StepUpDialog'

export interface PlanRunnerProps {
  plan: AgentPlan
  /** Shown in the step-up prompt, e.g. the agent or team name. */
  subjectLabel: string
  /** Back to the form that produced the plan. */
  onBack: () => void
  /** Leave the review (Done / Close). */
  onDone: () => void
  /** Called after an apply or rollback changes anything, with the outcome. */
  onChanged?: (outcome: ApplyOutcome) => void
}

/**
 * Plan -> approve -> apply (-> roll back) for lifecycle plans. Approve, apply and
 * rollback are ActionBus actions with a `confirm` requirement, so the bus runs
 * IdentityConfirm before the client is called; same path as the B2 editor.
 */
export function PlanRunner({ plan: initial, subjectLabel, onBack, onDone, onChanged }: PlanRunnerProps) {
  const edit = useAgentEdit()
  const [plan, setPlan] = useState(initial)
  const [outcome, setOutcome] = useState<ApplyOutcome | null>(null)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<string | null>(null)
  if (!edit) return null

  const run = async (kind: AgentActionKind) => {
    setBusy(true)
    setMessage(null)
    try {
      const res = await edit.bus.dispatch(agentPlanAction(kind, plan, subjectLabel))
      if (res.status === 'denied') setMessage(`Not confirmed: ${res.message ?? 'identity check declined'}. Nothing changed.`)
      else if (res.status === 'unhandled') setMessage('Agent changes are not wired to the action bus.')
      else if (kind === AGENT_ACTIONS.approve) {
        if (res.status === 'ok') setPlan(res.data as AgentPlan)
        else setMessage(res.message ?? 'Approval failed')
      } else {
        const out = res.data as ApplyOutcome | undefined
        if (out) {
          setPlan(out.plan)
          setOutcome(out)
          if (out.status === 'applied' || out.status === 'rolled_back') onChanged?.(out)
        } else setMessage(res.message ?? 'Request failed')
      }
    } catch (e) {
      setMessage(e instanceof Error ? e.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <StepUpHost identity={edit.identity} />
      <PlanReview
        plan={plan}
        outcome={outcome}
        busy={busy}
        message={message}
        onBack={onBack}
        onApprove={() => run(AGENT_ACTIONS.approve)}
        onApply={() => run(AGENT_ACTIONS.apply)}
        onRollback={() => run(AGENT_ACTIONS.rollback)}
        onDone={onDone}
      />
    </>
  )
}
