import type { MessageIntent } from './types'

export interface MessageTemplate {
  id: string
  intent: Exclude<MessageIntent, 'message'>
  label: string
  /** Text with {placeholders} filled from TemplateVars. */
  body: string
  /** Request update only: hint of the reply wanted. */
  expectedResponse?: string
}

export interface TemplateVars {
  agent?: string
  /** Subject being asked about, e.g. a task id or PR. */
  subject?: string
  operator?: string
}

export const NUDGE_TEMPLATES: MessageTemplate[] = [
  { id: 'nudge-ping', intent: 'nudge', label: 'Ping', body: 'Ping from {operator}: are you still on this?' },
  { id: 'nudge-blocked', intent: 'nudge', label: 'Blocked?', body: 'Nudge: you look blocked{on}. Anything you need from me?' },
  { id: 'nudge-wrap', intent: 'nudge', label: 'Wrap up', body: 'Nudge: please wrap up{on} and report back when you can.' },
]

export const UPDATE_TEMPLATES: MessageTemplate[] = [
  { id: 'update-status', intent: 'update_request', label: 'Status update', body: 'Please send a short status update{on}: done, in progress, blocked.', expectedResponse: 'three short bullets, within 10 minutes' },
  { id: 'update-eta', intent: 'update_request', label: 'ETA', body: 'What is your ETA{on}? Reply with a time and any risk to it.', expectedResponse: 'one line with a time' },
  { id: 'update-blockers', intent: 'update_request', label: 'Blockers', body: 'List anything blocking you{on}, and what would unblock it.', expectedResponse: 'list, or "none"' },
]

export function templatesFor(intent: Exclude<MessageIntent, 'message'>): MessageTemplate[] {
  return intent === 'nudge' ? NUDGE_TEMPLATES : UPDATE_TEMPLATES
}

/** Fills {operator}, {agent}, {subject}; {on} becomes " on <subject>" or nothing. */
export function renderTemplate(t: MessageTemplate, vars: TemplateVars = {}): string {
  return t.body
    .replaceAll('{on}', vars.subject ? ` on ${vars.subject}` : '')
    .replaceAll('{operator}', vars.operator ?? 'the operator')
    .replaceAll('{agent}', vars.agent ?? 'agent')
    .replaceAll('{subject}', vars.subject ?? 'this')
}
