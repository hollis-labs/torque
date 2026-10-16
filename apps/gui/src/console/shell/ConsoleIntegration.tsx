import { useState, type ReactNode } from 'react'
import { useApi } from '@/hooks/use-api'
import { AlertHost } from '../alerts/AlertHost'
import { CardRuntimeProvider } from '../cards/surfaces'
import { createDefaultCardRegistry } from '../cards/kinds'
import { createMockChatClient } from '../chat/mock'
import { createMailChatClient } from '../chat/mail'
import { ChatProvider } from '../chat/context'
import type { ConsoleMode } from '../events/mode'
import { registerHitlKinds } from '../hitl/kinds'
import { registerMetricCardKinds } from '../metrics/card-kinds'
import { createMailMessagingClient } from '../messaging/adapter'
import { MessagingProvider } from '../messaging/context'
import { createMockMessagingClient, OPERATOR_ADDRESS } from '../messaging/mock'
import { AgentUnreadProvider } from '../messaging/unread'
import { DEFAULT_RULE_PACK } from '../rules/default-pack'
import { registerWorkflowKinds } from '../workflow/kinds'
import { ChatDockProvider } from './chat-dock'

/**
 * Everything the Console adds under <ConsoleProvider>, in the one order that works:
 *   Messaging (A2 replies, M1 actions, unread poll) > Chat > card kinds > AlertHost > chat dock.
 * AutolinkProvider sits above ConsoleShell (ShellAutolink) so chat refs chip.
 * Fixture mode wires the mocks; live mode wires Tether mail through the ApiClient
 * (chat has no roster yet: threads open from agent chips / the dock).
 */
export function ConsoleIntegration({ mode, children }: { mode: ConsoleMode; children: ReactNode }) {
  const api = useApi()
  const [{ messaging, chat, registry }] = useState(() => {
    const messaging = mode === 'fixture' ? createMockMessagingClient() : createMailMessagingClient(api, { from: OPERATOR_ADDRESS })
    const chat = mode === 'fixture' ? createMockChatClient() : createMailChatClient(messaging, { operator: OPERATOR_ADDRESS, agents: [] })
    // Card kinds only: no toast/modal surface (AlertHost owns those) and no mock HITL/workflow clients.
    const registry = createDefaultCardRegistry()
    registerMetricCardKinds(registry)
    registerHitlKinds(registry)
    registerWorkflowKinds(registry)
    return { messaging, chat, registry }
  })
  return (
    <MessagingProvider client={messaging} operator={OPERATOR_ADDRESS}>
      <AgentUnreadProvider>
        <ChatProvider client={chat} operator={OPERATOR_ADDRESS}>
          <CardRuntimeProvider registry={registry} mountSurfaces={false}>
            <AlertHost rules={DEFAULT_RULE_PACK}>
              <ChatDockProvider>{children}</ChatDockProvider>
            </AlertHost>
          </CardRuntimeProvider>
        </ChatProvider>
      </AgentUnreadProvider>
    </MessagingProvider>
  )
}
