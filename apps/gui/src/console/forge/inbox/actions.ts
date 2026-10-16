// ActionBus wiring for the inbox. Triage (snooze / dismiss / settle) is a plain
// StateStore write; ASSIGN acts on the forge and dispatches work to an agent, so
// it goes through the bus with an IdentityConfirm requirement.
import type { ActionBus, ConsoleAction } from '../../seams/action-bus'
import type { StateStore } from '../../seams/state-store'
import type { MessagingClient, Recipient, SentMessage } from '../../messaging/types'
import type { AgentRecord } from '../../agents/types'
import type { ForgeClient } from '../client'
import { prKey, type PendingPrSummary } from '../types'
import {
  ASSIGNED_RESOLUTION,
  ASSIGN_TASKS,
  assignmentKey,
  assignmentSubject,
  buildAssignmentPayload,
  encodeAssignmentNote,
  parseAssignmentState,
  renderAssignmentBody,
  type AssignmentRecord,
  type AssignTask,
} from './assign'

export const PR_ASSIGN_ACTION = 'pr.assign'
export const PR_ASSIGN_SCOPES = { forge: 'forge:assign', message: 'message:send' } as const

export interface AssignParams {
  pr: PendingPrSummary
  agent: Pick<AgentRecord['identity'], 'id' | 'name' | 'urn'>
  task: AssignTask
  note?: string
  /** Also set the agent as the PR assignee on the forge (default true). */
  setForgeAssignee?: boolean
  /** Assign again although an identical assignment is recorded. */
  force?: boolean
}

export function prSubject(pr: PendingPrSummary) {
  return { type: 'pr', id: prKey(pr.ref), label: `${pr.ref.repo.owner}/${pr.ref.repo.name}#${pr.ref.number}` }
}

/** Builds the bus action, including the IdentityConfirm requirement. */
export function assignAction(p: AssignParams): ConsoleAction {
  const subject = prSubject(p.pr)
  const forge = p.setForgeAssignee !== false
  const task = ASSIGN_TASKS.find((t) => t.id === p.task)
  return {
    kind: PR_ASSIGN_ACTION,
    subject,
    params: { ...p, setForgeAssignee: forge },
    confirm: {
      what: {
        action: PR_ASSIGN_ACTION,
        title: `Assign ${subject.label} to ${p.agent.name}`,
        summary: `${task?.label ?? p.task}${forge ? '; sets the PR assignee on the forge and' : ''} sends the agent a task message.`,
        subject,
      },
      scope: forge ? [PR_ASSIGN_SCOPES.forge, PR_ASSIGN_SCOPES.message] : [PR_ASSIGN_SCOPES.message],
    },
  }
}

export interface InboxActionDeps {
  forge: ForgeClient
  messaging: MessagingClient
  stateStore: StateStore
  /** Agent -> forge login. Default: the agent slug (backend need: explicit mapping). */
  forgeLogin?: (agent: AssignParams['agent']) => string
  /** Recipient for an agent without a registered URN. */
  fallbackAddress?: (agentId: string) => string
  operator?: string
  now?: () => number
}

export interface AssignOutcome {
  sent: SentMessage
  record: AssignmentRecord
  forgeAssigned: boolean
}

export function recipientFor(agent: AssignParams['agent'], fallback: (id: string) => string = (id) => `msg://agent/console/${id}`): Recipient {
  return { address: agent.urn ?? fallback(agent.id), label: agent.name, kind: 'agent', agentId: agent.id }
}

export function registerInboxActions(bus: ActionBus, deps: InboxActionDeps): () => void {
  const now = deps.now ?? Date.now
  return bus.register(PR_ASSIGN_ACTION, async (action, ctx) => {
    const p = action.params as unknown as AssignParams
    if (!p?.pr || !p.agent || !p.task) return { status: 'error', message: 'Missing PR, agent or task' }
    const key = assignmentKey(prKey(p.pr.ref), p.agent.id, p.task)

    if (!p.force) {
      const rows = await deps.stateStore.load()
      const dup = rows.find((r) => r.key === key && parseAssignmentState(r))
      if (dup) return { status: 'error', message: `Already assigned to ${p.agent.name} for this task. Assign again to repeat it.` }
    }

    const login = (deps.forgeLogin ?? ((a) => a.id))(p.agent)
    const logins = p.pr.assignees.map((a) => a.login)
    let forgeAssigned = false
    if (p.setForgeAssignee !== false) {
      if (!logins.includes(login)) await deps.forge.assign(p.pr.ref, { assignees: [...logins, login] })
      forgeAssigned = true
    }

    const payload = buildAssignmentPayload(p.pr, p.task, p.agent.id, { note: p.note, assignedBy: ctx.confirmation?.who.id ?? deps.operator })
    let sent: SentMessage
    try {
      sent = await deps.messaging.send({
        to: recipientFor(p.agent, deps.fallbackAddress),
        intent: 'message',
        subject: assignmentSubject(payload),
        body: renderAssignmentBody(payload),
        templateId: 'pr_assignment',
      })
    } catch (e) {
      return { status: 'error', message: `${forgeAssigned ? 'PR assignee set, but the' : 'The'} task message failed: ${e instanceof Error ? e.message : 'send failed'}` }
    }
    if (sent.state === 'failed') {
      return { status: 'error', message: `${forgeAssigned ? 'PR assignee set, but the' : 'The'} task message to ${p.agent.name} failed to send.` }
    }

    const row = await deps.stateStore.apply({
      type: 'settle',
      key,
      resolution: ASSIGNED_RESOLUTION,
      note: encodeAssignmentNote({ messageId: sent.id, forgeAssigned }),
    })
    const record = parseAssignmentState({ ...row, updatedAt: row.updatedAt || now() }) as AssignmentRecord
    const out: AssignOutcome = { sent, record, forgeAssigned }
    return { status: 'ok', data: out, message: `Assigned to ${p.agent.name}` }
  })
}
