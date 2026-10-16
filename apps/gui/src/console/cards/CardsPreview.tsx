// Self-contained card showcase for pages/WidgetPreviewPage: own fixture feed,
// mock identity step-up and mock action handlers. Touches no shared state.
import { useState } from 'react'
import { FixtureSource } from '../events/fixture-source'
import { ConsoleProvider } from '../events/provider'
import { createActionBus } from '../seams/action-bus'
import { MockIdentityConfirm } from '../seams/identity-confirm'
import { cardFixtureEvents } from './fixtures'
import { registerMockCardHandlers } from './mock-handlers'
import { CardInlineSlot, CardRuntimeProvider, CardTray } from './surfaces'
import { useCardHost } from './runtime'

function Controls() {
  const host = useCardHost()
  return (
    <div className="flex gap-2 border-b border-zinc-800 px-4 py-2 font-mono text-[11px]">
      <button type="button" className="border border-zinc-700 px-2 py-0.5 hover:bg-zinc-800" onClick={() => host.openModal('thread:thr-1')}>open modal</button>
      <button type="button" className="border border-zinc-700 px-2 py-0.5 hover:bg-zinc-800" onClick={() => host.toast('approval:appr-1')}>toast approval</button>
    </div>
  )
}

export function CardsPreview() {
  const [{ source, identity, bus }] = useState(() => {
    const source = new FixtureSource({ intervalMs: 0, initial: 0 })
    cardFixtureEvents(Date.now()).forEach((e) => source.emit(e))
    const identity = new MockIdentityConfirm({ mode: 'auto-grant' })
    const bus = createActionBus({ identity })
    registerMockCardHandlers(bus, { latencyMs: 300 })
    return { source, identity, bus }
  })
  return (
    <ConsoleProvider source={source} identity={identity} actionBus={bus} resyncMs={0}>
      <CardRuntimeProvider>
        <div className="border border-zinc-800">
          <Controls />
          <div className="grid md:grid-cols-2">
            <CardTray title="Tray (compact, expands in place)" />
            <div className="border-l border-zinc-800">
              <p className="border-b border-zinc-800 px-4 py-2 font-mono text-[10px] uppercase tracking-[0.2em] text-zinc-400">Inline slot (agent-alpha)</p>
              <CardInlineSlot subject={{ type: 'agent', id: 'agent-alpha' }} density="expanded" hideWhenEmpty={false} />
            </div>
          </div>
        </div>
      </CardRuntimeProvider>
    </ConsoleProvider>
  )
}
