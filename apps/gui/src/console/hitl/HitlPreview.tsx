// Self-contained HITL showcase for pages/WidgetPreviewPage: own fixture client,
// mock identity step-up (manual dialog) and a registry with the HITL kinds.
import { useState, useSyncExternalStore } from 'react'
import { CardInlineSlot, CardModal, CardRuntimeProvider, CardTray } from '../cards/surfaces'
import { createDefaultCardRegistry } from '../cards/kinds'
import { FixtureSource } from '../events/fixture-source'
import { ConsoleProvider } from '../events/provider'
import { createActionBus } from '../seams/action-bus'
import { MockIdentityConfirm } from '../seams/identity-confirm'
import { MockHitlClient } from './client'
import { hitlFixtures } from './fixtures'
import { HitlProvider } from './provider'
import { registerHitlKinds } from './kinds'

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
        <button type="button" onClick={() => identity.resolve(first.id, 'granted')} className="border border-amber-300 bg-amber-300 px-2.5 py-1 font-mono text-[11px] uppercase tracking-wider text-zinc-950">Confirm</button>
        <button type="button" onClick={() => identity.resolve(first.id, 'denied', 'declined')} className="border border-zinc-700 px-2.5 py-1 font-mono text-[11px] uppercase tracking-wider text-zinc-200">Deny</button>
      </div>
    </div>
  )
}

export function HitlPreview() {
  const [{ source, identity, bus, registry, client }] = useState(() => {
    const identity = new MockIdentityConfirm({ mode: 'manual', who: { id: 'chrispian', label: 'Chrispian' } })
    const registry = createDefaultCardRegistry()
    registerHitlKinds(registry)
    return {
      source: new FixtureSource({ intervalMs: 0, initial: 0 }),
      identity,
      bus: createActionBus({ identity }),
      registry,
      client: new MockHitlClient(hitlFixtures(Date.now()), { latencyMs: 250 }),
    }
  })
  return (
    <ConsoleProvider source={source} identity={identity} actionBus={bus} resyncMs={0}>
      <CardRuntimeProvider registry={registry} mountSurfaces={false}>
        <HitlProvider client={client}>
          <div className="border border-zinc-800">
            <ConfirmDialog identity={identity} />
            <div className="grid md:grid-cols-2">
              <CardTray title="HITL tray (expands in place)" kinds={registry.list().filter((k) => k.id.startsWith('hitl.')).map((k) => k.id)} includeClosed />
              <div className="border-l border-zinc-800">
                <p className="border-b border-zinc-800 px-4 py-2 font-mono text-[10px] uppercase tracking-[0.2em] text-zinc-400">Inline slot (hitl subject)</p>
                <CardInlineSlot subject={{ type: 'hitl' }} density="inline" hideWhenEmpty={false} />
              </div>
            </div>
          </div>
          <CardModal />
        </HitlProvider>
      </CardRuntimeProvider>
    </ConsoleProvider>
  )
}
