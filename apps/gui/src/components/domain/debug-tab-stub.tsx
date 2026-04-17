const PLANNED_EVENTS: Array<{ key: string; label: string; hint: string }> = [
  { key: 'status_transition', label: 'Status transition', hint: 'todo → doing → review → done, with actor + reason' },
  { key: 'checkpoint_emitted', label: 'Checkpoint emitted', hint: 'blocking/non-blocking prompts the agent paused on' },
  { key: 'checkpoint_responded', label: 'Checkpoint responded', hint: 'responder source_type + payload excerpt' },
  { key: 'budget_decision', label: 'Budget decision', hint: 'cost / token / duration cap reached — what the scheduler did' },
  { key: 'retry_decision', label: 'Retry decision', hint: 'why the run was re-dispatched vs. blocked vs. failed' },
  { key: 'dep_gate', label: 'Dependency gate', hint: 'which depends_on blocked the task from running' },
  { key: 'subtodo_gate', label: 'Subtodo gate', hint: 'required items that blocked done→review transition' },
  { key: 'guardrail_hit', label: 'Guardrail hit', hint: 'policy/permission violation captured by the harness' },
]

export function DebugTabStub() {
  return (
    <div className="flex flex-col gap-4 px-1 py-2">
      <div className="rounded-md border border-zinc-800/60 bg-zinc-950 px-4 py-6">
        <div className="flex items-center gap-2">
          <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">Debug</span>
          <span className="text-[10px] uppercase tracking-[.18em] text-amber-400/80">coming soon</span>
        </div>
        <p className="mt-2 text-[13px] text-zinc-300">
          A time-ordered trace of scheduler and agent decisions — why the task moved, why it paused,
          why it retried. Exposed here so agents can reconstruct a run without grepping logs.
        </p>
      </div>

      <div className="rounded-md border border-zinc-800/60 bg-zinc-950">
        <div className="border-b border-zinc-800/60 px-4 py-2">
          <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">Planned decision events</span>
        </div>
        <ul className="divide-y divide-zinc-800/40">
          {PLANNED_EVENTS.map((e) => (
            <li key={e.key} className="flex flex-col gap-0.5 px-4 py-2">
              <span className="text-[12px] text-zinc-200">{e.label}</span>
              <span className="text-[11px] text-zinc-500">{e.hint}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}
