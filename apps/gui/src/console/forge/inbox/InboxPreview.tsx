// Self-contained showcase for pages/WidgetPreviewPage: mock forge, agents,
// messaging and an auto-grant identity. Renders inside the page's existing router.
import { useState } from 'react'
import { FixtureSource } from '../../events/fixture-source'
import { ConsoleProvider } from '../../events/provider'
import { createMockMessagingClient } from '../../messaging/mock'
import { MessagingProvider } from '../../messaging/context'
import { createActionBus } from '../../seams/action-bus'
import { MockIdentityConfirm } from '../../seams/identity-confirm'
import { createMockForgeClient } from '../mock'
import { PendingPrInbox } from './PendingPrInbox'

export function PendingPrInboxPreview() {
  const [{ source, identity, bus, forge, messaging }] = useState(() => {
    const identity = new MockIdentityConfirm({ mode: 'auto-grant' })
    return {
      source: new FixtureSource({ intervalMs: 0, initial: 0 }),
      identity,
      bus: createActionBus({ identity }),
      forge: createMockForgeClient(),
      messaging: createMockMessagingClient(),
    }
  })
  return (
      <ConsoleProvider source={source} identity={identity} actionBus={bus} resyncMs={0}>
        <MessagingProvider client={messaging} actionBus={bus}>
          <PendingPrInbox forge={forge} pollMs={0} />
        </MessagingProvider>
      </ConsoleProvider>
  )
}
