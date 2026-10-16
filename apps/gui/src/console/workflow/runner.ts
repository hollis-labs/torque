// FlowRunner: framework-free store that runs FlowDefinitions against the
// Console event feed. It owns instances, exposes their cards and registers the
// generic "workflow.flow.act" ActionBus handler. Step-up for a step action is
// applied to its inner dispatch, so the user confirms once.
import type { ConsoleEvent } from '../events/types'
import type { ActionBus } from '../seams/action-bus'
import type { CardInstance } from '../cards/types'
import { flowAct, flowCard, flowOnEvent, flowStart, flowTimeout, FLOW_ACT_KIND, type FlowDefinition, type FlowInstance } from './flow'

export interface FlowRunner {
  /** Feed one event (idempotent per event id). */
  onEvent(e: ConsoleEvent): void
  /** Fire timeouts that are due. */
  tick(now?: number): void
  instances(): FlowInstance[]
  cards(): CardInstance[]
  subscribe(l: () => void): () => void
  /** Registers the ActionBus handler; returns unregister. */
  attach(bus: ActionBus): () => void
}

export function createFlowRunner(defs: FlowDefinition[], opts: { now?: () => number } = {}): FlowRunner {
  const now = opts.now ?? Date.now
  const byDef = new Map(defs.map((d) => [d.id, d]))
  let insts: FlowInstance[] = []
  let seen = new Set<string>()
  let seq = 0
  const ls = new Set<() => void>()
  const emit = () => ls.forEach((l) => l())
  let snapshot: CardInstance[] = []
  const rebuild = () => {
    snapshot = insts.map((i) => flowCard(byDef.get(i.defId)!, i, now())).filter((c): c is CardInstance => !!c)
    emit()
  }

  const runner: FlowRunner = {
    onEvent(e) {
      if (seen.has(e.id)) return
      seen.add(e.id)
      if (seen.size > 5000) seen = new Set([...seen].slice(-2500))
      let changed = false
      insts = insts.map((i) => {
        const def = byDef.get(i.defId)
        const n = def ? flowOnEvent(def, i, e) : i
        if (n !== i) changed = true
        return n
      })
      for (const def of defs) {
        const startKey = (id?: string) => insts.some((i) => i.defId === def.id && i.runId === id && i.status !== 'ended')
        const inst = flowStart(def, e, `${def.id}#${++seq}`)
        if (inst && !startKey(inst.runId)) {
          insts = [...insts, inst]
          changed = true
        }
      }
      if (changed) rebuild()
    },
    tick(t = now()) {
      let changed = false
      insts = insts.map((i) => {
        const def = byDef.get(i.defId)
        const n = def ? flowTimeout(def, i, t) : i
        if (n !== i) changed = true
        return n
      })
      if (changed) rebuild()
    },
    instances: () => insts,
    cards: () => snapshot,
    subscribe(l) {
      ls.add(l)
      return () => ls.delete(l)
    },
    attach(bus) {
      return bus.register(FLOW_ACT_KIND, async (a) => {
        const id = a.params?.instanceId
        const actionId = a.params?.actionId
        const input = typeof a.params?.input === 'string' ? a.params.input : undefined
        const inst = insts.find((i) => i.id === id)
        const def = inst && byDef.get(inst.defId)
        if (!inst || !def || typeof actionId !== 'string') throw new Error('Flow step is no longer active')
        let out
        try {
          out = flowAct(def, inst, actionId, input, now())
        } catch (e) {
          throw e instanceof Error ? e : new Error('Action unavailable')
        }
        if (out.dispatch) {
          const scope = out.action?.confirmScope
          const res = await bus.dispatch({
            kind: out.dispatch.kind,
            params: out.dispatch.params,
            subject: inst.runId ? { type: 'run', id: inst.runId } : undefined,
            ...(scope ? { confirm: { what: { action: `flow.${def.id}.${out.action!.id}`, title: out.action!.label, subject: inst.runId ? { type: 'run', id: inst.runId } : undefined }, scope } } : {}),
          })
          // The step only advances when the dispatched action succeeded.
          if (res.status !== 'ok') throw new Error(res.message ?? `Action ${res.status}`)
        }
        insts = insts.map((i) => (i.id === inst.id ? out.inst : i))
        rebuild()
        return { status: 'ok', message: out.action?.label ? `${out.action.label}: done` : 'Done' }
      })
    },
  }
  return runner
}
