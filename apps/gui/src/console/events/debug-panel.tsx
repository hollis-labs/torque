import { useConsoleState, useConsoleStore } from './hooks'
import { FixtureSource } from './fixture-source'

/** Dev-only inspector for the Console feed. Shows status and the newest events; fixture sources get tick/gap buttons. */
export function ConsoleDebugPanel({ limit = 12 }: { limit?: number }) {
  const store = useConsoleStore()
  const state = useConsoleState()
  const fixture = store.source instanceof FixtureSource ? store.source : null
  const rows = [...state.events].reverse().slice(0, limit)
  return (
    <div className="font-mono text-xs">
      <div className="flex flex-wrap items-center gap-3 border-b border-border/60 pb-2">
        <span>source: {store.source.id}</span>
        <span>status: {state.status}</span>
        <span>ready: {String(state.ready)}</span>
        <span>events: {state.events.length}</span>
        <span>cursor: {state.cursor ?? '-'}</span>
        {state.error && <span className="text-destructive">error: {state.error}</span>}
        <button type="button" className="border border-border px-2 py-0.5" onClick={() => void store.resync()}>resync</button>
        {fixture && (
          <>
            <button type="button" className="border border-border px-2 py-0.5" onClick={() => fixture.tick()}>emit</button>
            <button type="button" className="border border-border px-2 py-0.5" onClick={() => fixture.simulateGap()}>gap</button>
          </>
        )}
      </div>
      <ul className="divide-y divide-border/60">
        {rows.map((e) => (
          <li key={e.id} className="flex gap-3 py-1">
            <span className="w-20 shrink-0 text-muted-foreground">{new Date(e.ts).toLocaleTimeString()}</span>
            <span className="w-14 shrink-0">{e.severity}</span>
            <span className="w-40 shrink-0">{e.kind}</span>
            <span className="truncate text-muted-foreground">{e.subjects.map((s) => s.label ?? s.id).join(', ')}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
