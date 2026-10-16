// Mounts the client, registers the HITL ActionBus handlers (under the Console
// ActionBus, so IdentityConfirm applies) and presents every request as a card
// through the CardHost. Mount once under <CardRuntimeProvider>.
import { useEffect, type ReactNode } from 'react'
import { useCardHost } from '../cards/runtime'
import { useConsoleSeams } from '../events/hooks'
import type { HitlClient } from './client'
import { registerHitlHandlers } from './handlers'
import { HitlClientContext, useHitlRequests } from './hooks'
import { requestToCard } from './model'

export function HitlProvider({ client, children, present = true }: { client: HitlClient; children: ReactNode; present?: boolean }) {
  const { actionBus } = useConsoleSeams()
  useEffect(() => registerHitlHandlers(actionBus, client), [actionBus, client])
  return (
    <HitlClientContext.Provider value={client}>
      {present && <HitlCardPresenter client={client} />}
      {children}
    </HitlClientContext.Provider>
  )
}

function HitlCardPresenter({ client }: { client: HitlClient }) {
  const host = useCardHost()
  const requests = useHitlRequests(client)
  useEffect(() => {
    const offs = requests.map((r) => host.present(requestToCard(r)))
    return () => offs.forEach((o) => o())
  }, [requests, host])
  return null
}
