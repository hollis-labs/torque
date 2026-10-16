import type { Alert } from './types'

/** Static sample alerts for previews and tests (one per severity / icon). */
export function sampleAlerts(now: number): Alert[] {
  const m = (n: number) => now - n * 60_000
  return [
    { id: 's-critical', severity: 'critical', icon: 'error', title: 'cerberus-github plugin down', detail: 'Health check failed 3 times on hub', source: 'cerberus · hub', ts: m(1), kind: 'sample', primary: { label: 'View agents', href: '/planner/agents' } },
    { id: 's-error', severity: 'error', icon: 'pr', title: 'hollis-labs/torque#241 checks failed', detail: 'build: tsc -b exited with 2 errors', source: 'agent-alpha · hub', ts: m(3), kind: 'sample', primary: { label: 'Open', href: '/planner/agents' } },
    { id: 's-warning', severity: 'warning', icon: 'approval', title: 'Approval requested', detail: 'Apply agent-bravo policy change (privileged)', source: 'agent-bravo', ts: m(8), kind: 'sample' },
    { id: 's-notice', severity: 'notice', icon: 'message', title: 'Need a decision', detail: 'Should I proceed with the schema change?', source: 'agent-charlie · laptop', ts: m(15), kind: 'sample' },
    { id: 's-info', severity: 'info', icon: 'task', title: 'CW-20261016-0100 → review', detail: 'Console foundation · was in_progress', source: 'torque', ts: m(42), kind: 'sample', primary: { label: 'Open task', href: '/tasks/CW-20261016-0100' } },
  ]
}
