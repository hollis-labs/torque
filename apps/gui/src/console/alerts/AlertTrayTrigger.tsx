import { Bell, BellOff } from 'lucide-react'
import { useOptionalAlertsController } from './context'
import { AlertBadge } from './AlertBadge'

/**
 * Bell for the shell's nav rail footer (sized h-9 w-9 like the other rail
 * buttons). Renders nothing when alerts are not enabled (no host above it).
 */
export function AlertTrayTrigger() {
  const c = useOptionalAlertsController()
  if (!c) return null
  const Icon = c.prefs.quiet ? BellOff : Bell
  return (
    <button
      type="button"
      data-alert-trigger=""
      aria-label={c.unread > 0 ? `Alerts, ${c.unread} unread` : 'Alerts'}
      aria-haspopup="dialog"
      aria-expanded={c.open}
      onClick={() => c.setOpen(!c.open)}
      className={`relative flex h-9 w-9 items-center justify-center text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 ${c.open ? 'bg-accent text-foreground' : ''}`}
    >
      <Icon aria-hidden="true" className="h-4 w-4" />
      <AlertBadge count={c.unread} severity={c.topUnread ?? 'info'} className="absolute -right-0.5 -top-0.5" />
    </button>
  )
}
