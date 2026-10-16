import { useEffect, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { ago } from '@/lib/agent-activity'
import { AlertIcon } from './AlertIcon'
import { AlertLinkButton } from './AlertLinkButton'
import { TOAST_EXPIRE_MS } from './policy'
import { SEVERITY_STYLE } from './severity-style'
import type { Alert } from './types'
import { LinkText } from '../autolink/LinkText'

export interface AlertToastProps {
  alert: Alert
  now: number
  /** Override auto-expire (ms, null = sticky). Defaults to the per-severity policy. */
  durationMs?: number | null
  onClose: () => void
  onRead?: () => void
  /** Opens the alert in the detail modal (A2). */
  onExpand?: () => void
}

/** Compact toast card. Expires per severity; hover or focus pauses the clock; Escape closes. */
export function AlertToast({ alert, now, durationMs, onClose, onRead, onExpand }: AlertToastProps) {
  const sev = SEVERITY_STYLE[alert.severity]
  const total = durationMs === undefined ? TOAST_EXPIRE_MS[alert.severity] : durationMs
  const [paused, setPaused] = useState(false)
  const remaining = useRef(total)
  const closeRef = useRef(onClose)
  useEffect(() => {
    closeRef.current = onClose
  })

  useEffect(() => {
    if (paused || remaining.current === null) return
    const left = remaining.current
    const started = Date.now()
    const t = setTimeout(() => closeRef.current(), left)
    return () => {
      clearTimeout(t)
      remaining.current = Math.max(0, left - (Date.now() - started))
    }
  }, [paused])

  const urgent = alert.severity === 'error' || alert.severity === 'critical'
  return (
    <div
      role={urgent ? 'alert' : 'status'}
      data-testid="alert-toast"
      data-severity={alert.severity}
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'Escape') onClose()
      }}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)}
      onBlur={() => setPaused(false)}
      className="pointer-events-auto relative flex gap-3 border border-zinc-700 bg-zinc-950 py-2.5 pl-4 pr-2 shadow-lg shadow-black/40 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300"
    >
      <span aria-hidden="true" className={`absolute inset-y-0 left-0 w-0.5 ${sev.bar}`} />
      <span className="pt-0.5">
        <AlertIcon icon={alert.icon} severity={alert.severity} />
      </span>
      <div className="min-w-0 flex-1">
        {onExpand ? (
          <button type="button" onClick={() => { onRead?.(); onClose(); onExpand() }} aria-label={`Open details: ${alert.title}`} className="block w-full truncate text-left font-mono text-xs font-semibold text-zinc-50 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">{alert.title}</button>
        ) : (
          <p className="truncate font-mono text-xs font-semibold text-zinc-50">{alert.title}</p>
        )}
        {alert.detail && <p className="truncate font-mono text-xs text-zinc-400"><LinkText surface="alerts" text={alert.detail} /></p>}
        <p className="mt-0.5 flex flex-wrap items-center gap-x-2 font-mono text-[10px] uppercase tracking-widest text-zinc-500">
          <span className={sev.text}>{sev.label}</span>
          <span className="normal-case tracking-normal">{alert.source}</span>
          <span className="normal-case tracking-normal">{ago(alert.ts, now)}</span>
        </p>
        {alert.primary && (
          <div className="mt-1.5">
            <AlertLinkButton
              link={alert.primary}
              onFollow={() => {
                onRead?.()
                onClose()
              }}
            />
          </div>
        )}
      </div>
      <button
        type="button"
        onClick={onClose}
        aria-label={`Close alert: ${alert.title}`}
        className="h-6 w-6 shrink-0 text-zinc-500 hover:text-zinc-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300"
      >
        <X aria-hidden="true" className="mx-auto h-3.5 w-3.5" />
      </button>
    </div>
  )
}
