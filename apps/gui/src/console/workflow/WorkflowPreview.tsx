// Self-contained workflow showcase for pages/WidgetPreviewPage: own fixture
// client, mock identity step-up (manual dialog), the sample flow and buttons
// that emit the events driving it (agent blocks / agent resumes).
import { useState, useSyncExternalStore } from 'react'
import { CardModal, CardRuntimeProvider, CardTray } from '../cards/surfaces'
import { createDefaultCardRegistry } from '../cards/kinds'
import { FixtureSource } from '../events/fixture-source'
import { ConsoleProvider } from '../events/provider'
import type { ConsoleEvent } from '../events/types'
import { createActionBus } from '../seams/action-bus'
import { MockIdentityConfirm } from '../seams/identity-confirm'
import { Btn } from '../hitl/parts'
import { MockWorkflowClient } from './client'
import { workflowFixtures } from './fixtures'
import { SAMPLE_BLOCKED_FLOW } from './flow'
import { registerWorkflowKinds, WORKFLOW_KINDS } from './kinds'
import { WorkflowProvider } from './provider'

const FLOWS = [SAMPLE_BLOCKED_FLOW]

function ConfirmDialog({ identity }: { identity: MockIdentityConfirm }) {
  const pending = useSyncExternalStore(identity.subscribe, () => identity.pending(), () => identity.pending())
  const first = pending[0]
  if (!first) return null
  return (
    <div role="alertdialog" aria-label="Confirm identity" className="border-b border-amber-300/60 bg-zinc-950 px-4 py-3">
      <p className="font-mono text-[10px] uppercase tracking-[0.2em] text-amber-300">Confirm your identity (mock)</p>
      <p className="mt-1 text-sm text-zinc-100">{first.request.what.title}</p>
      <p className="font-mono text-[11px] text-zinc-500">scope: {first.request.scope.join(', ')}</p>
      <div className="flex gap-2 pt-2">
        <Btn intent="primary" onClick={() => identity.resolve(first.id, 'granted')}>Confirm</Btn>
        <Btn onClick={() => identity.resolve(first.id, 'denied', 'declined')}>Deny</Btn>
      </div>
    </div>
  )
}

const stateEvent = (to: string, extra: Record<string, unknown> = {}): ConsoleEvent => ({
  id: `prev-${to}-${Date.now()}`, ts: Date.now(), kind: 'run.state_changed', source: { id: 'preview' }, subjects: [{ type: 'run', id: 'run-3', label: 'Nightly dependency audit' }],
  severity: to === 'blocked' ? 'warning' : 'info', payload: { runId: 'run-3', to, ...extra }, actions: [], extra: {},
})

export function WorkflowPreview() {
  const [{ source, identity, bus, registry, client }] = useState(() => {
    const identity = new MockIdentityConfirm({ mode: 'manual', who: { id: 'chrispian', label: 'Chrispian' } })
    const registry = createDefaultCardRegistry()
    registerWorkflowKinds(registry)
    return {
      source: new FixtureSource({ intervalMs: 0, initial: 0 }),
      identity,
      bus: createActionBus({ identity }),
      registry,
      client: new MockWorkflowClient(workflowFixtures(Date.now()), { latencyMs: 250 }),
    }
  })
  return (
    <ConsoleProvider source={source} identity={identity} actionBus={bus} resyncMs={0}>
      <CardRuntimeProvider registry={registry} mountSurfaces={false}>
        <WorkflowProvider client={client} flows={FLOWS}>
          <div className="border border-zinc-800">
            <ConfirmDialog identity={identity} />
            <div className="flex flex-wrap items-center gap-2 border-b border-zinc-800 px-4 py-2">
              <span className="font-mono text-[10px] uppercase tracking-[0.2em] text-zinc-400">Sample flow</span>
              <Btn onClick={() => source.emit(stateEvent('blocked', { reason: 'lint failing' }))}>1. Agent blocks</Btn>
              <Btn onClick={() => source.emit(stateEvent('running'))}>2. Agent resumes</Btn>
              <span className="font-mono text-[10px] text-zinc-500">blocks -&gt; alert -&gt; decision -&gt; redirect (step-up) -&gt; resolved</span>
            </div>
            <CardTray title="Workflow tray (expands in place)" kinds={WORKFLOW_KINDS.map((k) => k.id)} includeClosed />
          </div>
          <CardModal />
        </WorkflowProvider>
      </CardRuntimeProvider>
    </ConsoleProvider>
  )
}
