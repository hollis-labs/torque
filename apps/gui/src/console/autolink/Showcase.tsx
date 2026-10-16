import { useMemo } from 'react'
import { buildPreviewCard, createMockResolver, createMockResolvers, getRefRegistry, MOCK_ENTITIES } from '../refs'
import { AutolinkProvider } from './Provider'
import { Linkified } from './Linkified'
import { PreviewCardView } from './PreviewCardView'


const SAMPLES: { title: string; text: string }[] = [
  {
    title: 'Mixed references',
    text: 'Blocked on CW-20261016-0101 under EP-20261016-0002 (project PRJ-20260416-0001). Ask @claude-code, see workstream:ws-agent-console and tesseract:project/torque/workspace/handoff#agent_console_gui_orchestrator_2026_10_16. Edited apps/gui/src/console/refs/types.ts:42, docs at https://base-ui.com/react/components/preview-card.',
  },
  { title: 'Not found', text: 'CW-20269999-0001 does not exist in Torque; its chip is struck through and the card says so.' },
  { title: 'Code is left alone', text: 'Run `torque task get CW-20261016-0101` to see it, then compare with CW-20261016-0101 here.' },
]

/** Storybook-style entries for the autolink renderer (R1). Mock-backed; hover, tab to focus, click for the drawer. */
export function AutolinkShowcase() {
  const resolvers = useMemo(
    () => ({
      ...createMockResolvers({ latencyMs: 400 }),
      'torque.task': createMockResolver(MOCK_ENTITIES['torque.task'], { latencyMs: 400 }),
    }),
    [],
  )
  const registry = getRefRegistry()
  const states = [
    ['loading', { status: 'loading' as const }],
    ['not-found', { status: 'not-found' as const, fetchedAt: 0 }],
    ['error', { status: 'error' as const, message: 'resolver unavailable', fetchedAt: 0 }],
    ['unresolved', { status: 'unsupported' as const }],
  ] as const
  return (
    <AutolinkProvider resolvers={resolvers}>
      <div className="space-y-4">
        {SAMPLES.map((s) => (
          <div key={s.title}>
            <h3 className="mb-1 font-mono text-[10px] uppercase tracking-widest text-zinc-500">{s.title}</h3>
            <p className="text-sm leading-7 text-zinc-200"><Linkified text={s.text} /></p>
          </div>
        ))}
        <div>
          <h3 className="mb-1 font-mono text-[10px] uppercase tracking-widest text-zinc-500">Card states</h3>
          <div className="grid gap-px border border-zinc-800 bg-zinc-800 sm:grid-cols-2 lg:grid-cols-4">
            {states.map(([name, state]) => (
              <div key={name} className="bg-zinc-950 p-3">
                <PreviewCardView card={buildPreviewCard(registry, { kind: 'torque.task', canonical: 'CW-20261016-0101' }, state)} onRetry={() => undefined} />
              </div>
            ))}
          </div>
        </div>
      </div>
    </AutolinkProvider>
  )
}
