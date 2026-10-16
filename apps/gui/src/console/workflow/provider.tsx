// Mounts the WorkflowClient, registers the workflow ActionBus handlers (under
// the Console ActionBus, so IdentityConfirm applies), feeds Console events to
// the client and the FlowRunner, and presents run cards and flow cards through
// the CardHost. Mount once under <CardRuntimeProvider>.
import { useEffect, useMemo, type ReactNode } from 'react'
import { useCardHost } from '../cards/runtime'
import { useConsoleSeams, useConsoleState } from '../events/hooks'
import type { WorkflowClient } from './client'
import type { FlowDefinition } from './flow'
import { createFlowRunner } from './runner'
import { registerWorkflowHandlers } from './handlers'
import { WorkflowClientContext, useWorkflowRuns } from './hooks'
import { runToCards } from './model'

export function WorkflowProvider({ client, flows = [], children, present = true, tickMs = 5000 }: { client: WorkflowClient; flows?: FlowDefinition[]; children: ReactNode; present?: boolean; tickMs?: number }) {
  const { actionBus } = useConsoleSeams()
  const runner = useMemo(() => createFlowRunner(flows), [flows])
  useEffect(() => registerWorkflowHandlers(actionBus, client), [actionBus, client])
  useEffect(() => runner.attach(actionBus), [actionBus, runner])
  useEffect(() => {
    if (!tickMs) return
    const t = setInterval(() => runner.tick(), tickMs)
    return () => clearInterval(t)
  }, [runner, tickMs])
  return (
    <WorkflowClientContext.Provider value={client}>
      <EventFeed client={client} runner={runner} />
      {present && <WorkflowCardPresenter client={client} runner={runner} />}
      {children}
    </WorkflowClientContext.Provider>
  )
}

/** Folds the shared Console event list into the client and the flows. No stream is opened. */
function EventFeed({ client, runner }: { client: WorkflowClient; runner: ReturnType<typeof createFlowRunner> }) {
  const { events } = useConsoleState()
  useEffect(() => {
    for (const e of events) {
      client.ingest?.(e)
      runner.onEvent(e)
    }
  }, [events, client, runner])
  return null
}

function WorkflowCardPresenter({ client, runner }: { client: WorkflowClient; runner: ReturnType<typeof createFlowRunner> }) {
  const host = useCardHost()
  const runs = useWorkflowRuns(client)
  useEffect(() => {
    const offs = runs.flatMap((r) => runToCards(r)).map((c) => host.present(c))
    return () => offs.forEach((o) => o())
  }, [runs, host])
  useEffect(() => {
    let offs: (() => void)[] = []
    const sync = () => {
      offs.forEach((o) => o())
      offs = runner.cards().map((c) => host.present(c))
    }
    sync()
    const unsub = runner.subscribe(sync)
    return () => {
      unsub()
      offs.forEach((o) => o())
    }
  }, [runner, host])
  return null
}
