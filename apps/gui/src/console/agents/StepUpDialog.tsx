import { useEffect, useSyncExternalStore } from 'react'
import { MockIdentityConfirm, type IdentityConfirm } from '../seams/identity-confirm'
import { Btn, Chip, Heading } from './ui'

function MockStepUp({ identity }: { identity: MockIdentityConfirm }) {
  const pending = useSyncExternalStore(identity.subscribe, () => identity.pending(), () => identity.pending())
  const head = pending[0]
  useEffect(() => {
    if (!head) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && identity.resolve(head.id, 'cancelled', 'dismissed')
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [head, identity])
  if (!head) return null
  const { request: r } = head
  const ttl = Math.round((r.ttlMs ?? 60_000) / 1000)
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
      <div role="dialog" aria-modal="true" aria-label="Confirm your identity" className="w-full max-w-md border border-amber-300/70 bg-zinc-950 shadow-xl">
        <div className="border-b border-zinc-800 px-5 py-3">
          <h2 className="font-mono text-sm uppercase tracking-widest text-amber-300">Confirm your identity</h2>
          <p className="mt-1 text-[12px] text-zinc-500">This change needs a fresh confirmation. Nothing happens until you confirm.</p>
        </div>
        <dl className="space-y-3 px-5 py-4 text-[13px]">
          <div>
            <Heading>Who</Heading>
            <dd className="text-zinc-200">{r.who?.label ?? r.who?.id ?? 'You (signed-in user)'}</dd>
          </div>
          <div>
            <Heading>What</Heading>
            <dd className="text-zinc-100">{r.what.title}</dd>
            {r.what.summary && <dd className="text-zinc-400">{r.what.summary}</dd>}
          </div>
          <div>
            <Heading>Scope</Heading>
            <dd className="flex flex-wrap gap-1.5">{r.scope.map((s) => <Chip key={s} tone={s.includes('privileged') || s.includes('grant') ? 'border-amber-400/60 text-amber-300' : undefined}>{s}</Chip>)}</dd>
          </div>
          <div>
            <Heading>Expires</Heading>
            <dd className="text-zinc-300">The grant is valid for {ttl} seconds after you confirm.</dd>
          </div>
        </dl>
        <div className="flex items-center gap-2 border-t border-zinc-800 px-5 py-3">
          <span className="mr-auto font-mono text-[10px] uppercase tracking-widest text-zinc-600">mock step-up</span>
          <Btn autoFocus onClick={() => identity.resolve(head.id, 'cancelled', 'cancelled by user')}>Cancel</Btn>
          <Btn tone="primary" onClick={() => identity.resolve(head.id, 'granted')}>Confirm</Btn>
        </div>
      </div>
    </div>
  )
}

/** Renders the step-up prompt for the mock; a real IdentityConfirm brings its own UI, so this renders nothing for it. */
export function StepUpHost({ identity }: { identity: IdentityConfirm }) {
  return identity instanceof MockIdentityConfirm && identity.mode === 'manual' ? <MockStepUp identity={identity} /> : null
}
