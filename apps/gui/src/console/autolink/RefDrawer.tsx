import { createElement, useEffect, useState } from 'react'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@hollis-labs/sysop-ui'
import { RefreshCw } from 'lucide-react'
import { buildPreviewCard, type RefIdentity } from '../refs'
import { useAutolink, useRefState } from './context'
import { refDetail } from './extensions'
import { PreviewCardView } from './PreviewCardView'

function DrawerBody({ target }: { target: RefIdentity }) {
  const { registry, client } = useAutolink()
  const state = useRefState(target.kind, target.canonical)
  useEffect(() => {
    client.request([target])
  }, [client, target])

  const card = buildPreviewCard(registry, target, state)
  const Detail = refDetail(target.kind)
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-auto px-4 pb-4">
      <PreviewCardView
        card={card}
        variant="drawer"
        onRetry={() => client.invalidate(target.kind, target.canonical)}
      >
        {Detail ? <div className="mt-3 border-t border-zinc-800/80 pt-3">{createElement(Detail, { refId: target, card })}</div> : undefined}
      </PreviewCardView>
      <button
        type="button"
        className="mt-4 inline-flex items-center gap-1.5 self-start font-mono text-[10px] uppercase tracking-wider text-zinc-500 hover:text-zinc-300 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300"
        onClick={() => client.invalidate(target.kind, target.canonical)}
      >
        <RefreshCw className="h-3 w-3" />
        Refresh
      </button>
    </div>
  )
}

/** Right-hand detail drawer. Esc, the close button and the backdrop dismiss it; focus is trapped and restored. */
export function RefDrawer({ target, onClose }: { target: RefIdentity | null; onClose: () => void }) {
  // Keep the last target mounted so the exit animation does not flash an empty panel.
  const [last, setLast] = useState<RefIdentity | null>(target)
  if (target && target !== last) setLast(target)
  const shown = target ?? last

  return (
    <Sheet open={target !== null} onOpenChange={(open) => !open && onClose()}>
      <SheetContent side="right" className="w-full gap-0 border-zinc-800 p-0 data-[side=right]:sm:max-w-md" data-testid="ref-drawer">
        <div className="border-b border-zinc-800/80 px-4 py-3 pr-12">
          <SheetTitle className="font-mono text-[11px] uppercase tracking-widest text-zinc-300">Details</SheetTitle>
          <SheetDescription className="sr-only">Details for {shown?.canonical}</SheetDescription>
        </div>
        {shown && <DrawerBody target={shown} />}
      </SheetContent>
    </Sheet>
  )
}
