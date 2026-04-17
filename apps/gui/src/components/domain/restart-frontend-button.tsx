import { useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'

/**
 * Admin-only affordance that triggers a backend `npm run build` for the
 * bundled GUI and surfaces the resulting dist hash. The endpoint itself
 * is localhost-gated, so this button is safe to render unconditionally
 * while the UI has no auth layer — non-local callers get a 403 which we
 * relay through the error toast.
 */
export function RestartFrontendButton() {
  const api = useApi()
  const [busy, setBusy] = useState(false)

  async function handleClick() {
    setBusy(true)
    try {
      const { hash, duration_ms } = await api.restartFrontend()
      const shortHash = hash.slice(0, 8)
      const durationS = (duration_ms / 1000).toFixed(1)
      notifySuccess(`Frontend rebuilt in ${durationS}s — ${shortHash}`)
    } catch (err) {
      notifyError(err, 'Frontend rebuild failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={handleClick}
      disabled={busy}
      className="h-7 gap-1.5 text-[11px] uppercase tracking-[.18em]"
      title="Rebuild the bundled GUI (admin)"
      aria-label="Rebuild bundled frontend"
    >
      <RefreshCw className={`h-3 w-3 ${busy ? 'animate-spin' : ''}`} aria-hidden />
      {busy ? 'Rebuilding…' : 'Rebuild UI'}
    </Button>
  )
}
