import { ago } from '@/lib/agent-activity'
import { AlertIcon } from './AlertIcon'
import { AlertLinkButton } from './AlertLinkButton'
import { SEVERITY_STYLE } from './severity-style'
import type { ItemStatus } from '../seams/state-store'
import type { Alert } from './types'
import { LinkText } from '../autolink/LinkText'

export interface AlertListItemProps {
  alert: Alert
  status?: ItemStatus
  snoozeUntil?: number
  now: number
  /** Called when the primary link is followed. */
  onRead?: () => void
  onDismiss?: () => void
  onSnooze?: () => void
  onRestore?: () => void
  /** Opens the alert in the detail modal (A2). */
  onExpand?: () => void
}

const BTN =
  'border border-transparent px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-widest text-zinc-400 hover:border-zinc-700 hover:text-zinc-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300'

/** One alert row: icon, severity stripe, title, one-line detail, source/age, primary link, state actions. */
export function AlertListItem({ alert, status = 'unread', snoozeUntil, now, onRead, onDismiss, onSnooze, onRestore, onExpand }: AlertListItemProps) {
  const sev = SEVERITY_STYLE[alert.severity]
  const unread = status === 'unread'
  const handled = status === 'dismissed' || status === 'snoozed' || status === 'settled'
  return (
    <li
      data-testid="alert-item"
      data-status={status}
      className={`relative flex gap-3 border-b border-zinc-800/80 py-2.5 pl-4 pr-3 ${handled ? 'opacity-60' : ''}`}
    >
      <span aria-hidden="true" className={`absolute inset-y-0 left-0 w-0.5 ${sev.bar}`} />
      <span className="pt-0.5">
        <AlertIcon icon={alert.icon} severity={alert.severity} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          {onExpand ? (
            <button type="button" onClick={onExpand} aria-label={`Open details: ${alert.title}`} className={`min-w-0 flex-1 truncate text-left font-mono text-xs hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 ${unread ? 'font-semibold text-zinc-50' : 'text-zinc-300'}`}>
              {alert.title}
            </button>
          ) : (
            <span className={`min-w-0 flex-1 truncate font-mono text-xs ${unread ? 'font-semibold text-zinc-50' : 'text-zinc-300'}`}>{alert.title}</span>
          )}
          {unread && <span className="h-1.5 w-1.5 shrink-0 bg-amber-300" role="img" aria-label="unread" />}
        </div>
        {alert.detail && <p className="truncate font-mono text-xs text-zinc-400"><LinkText surface="alerts" text={alert.detail} /></p>}
        <p className="mt-0.5 flex flex-wrap items-center gap-x-2 font-mono text-[10px] uppercase tracking-widest text-zinc-500">
          <span className={sev.text}>{sev.label}</span>
          <span className="normal-case tracking-normal">{alert.source}</span>
          <span className="normal-case tracking-normal">{ago(alert.ts, now)}</span>
          {status === 'snoozed' && snoozeUntil && <span className="normal-case tracking-normal">snoozed until {new Date(snoozeUntil).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>}
          {status === 'dismissed' && <span className="normal-case tracking-normal">dismissed</span>}
        </p>
        {(alert.primary || onDismiss || onSnooze || onRestore) && (
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            {alert.primary && <AlertLinkButton link={alert.primary} onFollow={onRead} />}
            {onSnooze && (
              <button type="button" className={BTN} onClick={onSnooze} aria-label={`Snooze for 1 hour: ${alert.title}`}>
                Snooze 1h
              </button>
            )}
            {onDismiss && (
              <button type="button" className={BTN} onClick={onDismiss} aria-label={`Dismiss: ${alert.title}`}>
                Dismiss
              </button>
            )}
            {onRestore && (
              <button type="button" className={BTN} onClick={onRestore} aria-label={`Restore: ${alert.title}`}>
                Restore
              </button>
            )}
          </div>
        )}
      </div>
    </li>
  )
}
