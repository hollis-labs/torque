import { useEffect, useRef, useState } from 'react'
import { BellOff, X } from 'lucide-react'
import { SEVERITY_ORDER } from '../events/types'
import { useAlertsController } from './context'
import { ConsolePrefsPanel } from '../prefs/ConsolePrefsPanel'
import { useRestoreFocus } from '../shell/restore-focus'
import { AlertListItem } from './AlertListItem'
import { SEVERITY_STYLE } from './severity-style'
import type { AlertSeverity } from './types'

const HOUR = 3_600_000
const BTN =
  'border border-zinc-700 px-2 py-0.5 font-mono text-[10px] uppercase tracking-widest text-zinc-300 hover:border-zinc-500 hover:bg-zinc-900 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300'

/** Persistent tray: active alerts and history, quiet mode and toast threshold. Escape or outside click closes. */
export function AlertTray() {
  const c = useAlertsController()
  const [tab, setTab] = useState<'active' | 'history'>('active')
  const [prefsOpen, setPrefsOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useRestoreFocus(c.open, '[data-alert-trigger]')

  useEffect(() => {
    if (!c.open) return
    const el = ref.current
    el?.focus()
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node
      // Clicks in the trigger or in a modal stacked above the tray (alert detail) are not "outside".
      if (el && !el.contains(t) && !(t as Element).closest?.('[data-alert-trigger], [data-overlay]')) c.setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [c.open])

  if (!c.open) return null
  const rows = tab === 'active' ? c.active : c.history
  return (
    <div
      ref={ref}
      role="dialog"
      aria-label="Alerts"
      tabIndex={-1}
      onKeyDown={(e) => {
        if (e.key === 'Escape') c.setOpen(false)
      }}
      className="fixed bottom-3 left-14 z-50 flex max-h-[min(36rem,calc(100vh-1.5rem))] w-[min(26rem,calc(100vw-4.5rem))] flex-col border border-zinc-700 bg-zinc-950 shadow-xl shadow-black/50 focus-visible:outline-none"
    >
      <header className="flex items-center gap-2 border-b border-zinc-800 px-4 py-2.5">
        <h2 className="flex-1 font-mono text-[10px] uppercase tracking-widest text-zinc-400">
          Alerts{c.unread > 0 ? ` · ${c.unread} unread` : ''}
        </h2>
        <button type="button" className={BTN} onClick={c.markAllRead} disabled={c.unread === 0}>
          Mark all read
        </button>
        <button type="button" onClick={() => c.setOpen(false)} aria-label="Close alerts" className="h-6 w-6 text-zinc-500 hover:text-zinc-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">
          <X aria-hidden="true" className="mx-auto h-3.5 w-3.5" />
        </button>
      </header>

      <div className="flex items-center gap-3 border-b border-zinc-800 px-4 py-2 font-mono text-[10px] uppercase tracking-widest">
        <label className="flex items-center gap-1.5 text-zinc-400">
          <input
            type="checkbox"
            checked={c.prefs.quiet}
            onChange={(e) => c.setPrefs({ quiet: e.target.checked })}
            className="h-3 w-3 accent-amber-300"
          />
          <BellOff aria-hidden="true" className="h-3 w-3" />
          Quiet
        </label>
        <label className="ml-auto flex items-center gap-1.5 text-zinc-400">
          Toast at
          <select
            value={c.prefs.toastMinSeverity}
            onChange={(e) => c.setPrefs({ toastMinSeverity: e.target.value as AlertSeverity })}
            disabled={c.prefs.quiet}
            className="border border-zinc-700 bg-zinc-950 px-1 py-0.5 text-zinc-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:opacity-50"
          >
            {SEVERITY_ORDER.slice(1).map((s) => (
              <option key={s} value={s}>
                {SEVERITY_STYLE[s].label.toLowerCase()}+
              </option>
            ))}
          </select>
        </label>
        <button type="button" aria-expanded={prefsOpen} onClick={() => setPrefsOpen((v) => !v)} className={BTN}>
          Prefs
        </button>
      </div>
      {prefsOpen && <ConsolePrefsPanel prefs={c.prefs} onChange={c.setPrefs} />}

      <div role="tablist" aria-label="Alert views" className="flex border-b border-zinc-800">
        {(['active', 'history'] as const).map((t) => {
          const n = t === 'active' ? c.active.length : c.history.length
          return (
            <button
              key={t}
              role="tab"
              type="button"
              aria-selected={tab === t}
              onClick={() => setTab(t)}
              className={`flex-1 border-b-2 px-4 py-2 font-mono text-[10px] uppercase tracking-widest focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-amber-300 ${tab === t ? 'border-amber-300 text-zinc-100' : 'border-transparent text-zinc-500 hover:text-zinc-300'}`}
            >
              {t} · {n}
            </button>
          )
        })}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {rows.length === 0 ? (
          <p className="px-4 py-8 text-center font-mono text-xs text-zinc-500">{tab === 'active' ? 'Nothing needs you right now.' : 'No dismissed or snoozed alerts.'}</p>
        ) : (
          <ul aria-label={tab === 'active' ? 'Active alerts' : 'Alert history'}>
            {rows.map(({ alert, status, snoozeUntil }) => (
              <AlertListItem
                key={alert.id}
                alert={alert}
                status={status}
                snoozeUntil={snoozeUntil}
                now={c.now}
                onRead={() => c.markRead(alert.id)}
                onDismiss={tab === 'active' ? () => c.dismiss(alert.id) : undefined}
                onSnooze={tab === 'active' ? () => c.snooze(alert.id, HOUR) : undefined}
                onRestore={tab === 'history' ? () => c.restore(alert.id) : undefined}
                onExpand={() => c.expand(alert.id)}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
