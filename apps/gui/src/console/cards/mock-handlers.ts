// Phase 1 is display-only: backends for approve/reject/reply do not exist. This
// registers mock handlers on the ActionBus so cards are exercisable in previews
// and tests. The real handlers replace them by re-registering the same kinds.
import type { ActionBus } from '../seams/action-bus'

export function registerMockCardHandlers(bus: ActionBus, opts: { latencyMs?: number; fail?: string[] } = {}): () => void {
  const wait = () => (opts.latencyMs ? new Promise((r) => setTimeout(r, opts.latencyMs)) : Promise.resolve())
  const offs = ['approve', 'reject', 'reply', 'navigate', 'api'].map((kind) =>
    bus.register(kind, async (a) => {
      await wait()
      if (opts.fail?.includes(kind)) throw new Error(`mock ${kind} failed`)
      return { status: 'ok', message: `Mock ${a.kind} recorded (no backend yet)` }
    }),
  )
  return () => offs.forEach((o) => o())
}
