// Registers the three messaging actions on the ActionBus. UI never calls the
// client directly; it dispatches these kinds so audit/toasts see every send.

import type { ActionBus, ActionHandler, ConsoleAction } from '../seams'
import { createNudgeLimiter, validateSend, type NudgeLimiter } from './guards'
import type { MessageIntent, MessagingClient, SendRequest, SentMessage } from './types'

export const MESSAGE_ACTIONS = {
  message: 'message.send',
  nudge: 'message.nudge',
  update_request: 'message.request_update',
} as const satisfies Record<MessageIntent, string>

export function messageAction(req: SendRequest): ConsoleAction {
  return {
    kind: MESSAGE_ACTIONS[req.intent],
    subject: { type: req.to.kind, id: req.to.agentId ?? req.to.address, label: req.to.label },
    params: { request: req },
  }
}

export function registerMessagingActions(
  bus: ActionBus,
  client: MessagingClient,
  limiter: NudgeLimiter = createNudgeLimiter(),
): () => void {
  const handler = (intent: MessageIntent): ActionHandler => async (action) => {
    const req = { ...(action.params?.request as SendRequest), intent }
    if (!req.to) return { status: 'error', message: 'No recipient' }
    const problem = validateSend(req)
    if (problem) return { status: 'error', message: problem }
    if (intent === 'nudge') {
      const wait = limiter.wait(req.to)
      if (wait > 0) return { status: 'error', message: `Already nudged ${req.to.label}. Try again in ${Math.ceil(wait / 1000)}s.` }
    }
    const sent: SentMessage = await client.send(req)
    if (intent === 'nudge') limiter.record(req.to)
    return { status: 'ok', data: sent, message: `Sent to ${req.to.label}` }
  }
  const offs = (Object.keys(MESSAGE_ACTIONS) as MessageIntent[]).map((i) => bus.register(MESSAGE_ACTIONS[i], handler(i)))
  return () => offs.forEach((o) => o())
}
