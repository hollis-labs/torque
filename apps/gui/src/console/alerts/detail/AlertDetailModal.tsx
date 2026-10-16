import { useEffect, useMemo, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { ago } from '@/lib/agent-activity'
import { Linkified } from '../../autolink'
import { useConsoleState } from '../../events/hooks'
import type { ConsoleEvent } from '../../events/types'
import { MessageComposer } from '../../messaging'
import { useMessaging } from '../../messaging/context'
import type { Recipient } from '../../messaging/types'
import type { ItemStatus } from '../../seams/state-store'
import { useAlertsController } from '../context'
import { AlertIcon } from '../AlertIcon'
import { AlertLinkButton } from '../AlertLinkButton'
import { safeHref } from '../policy'
import { SEVERITY_STYLE } from '../severity-style'
import { defaultAlertMapper } from '../mapping'
import type { Alert, AlertMapper } from '../types'
import { DEFER_CONTEXTS, defaultAlertActions, deferPresets, snoozePresets, type AlertActionId, type AlertActionResolver, type AlertActionSpec } from './actions'
import { relatedEvents, replyTarget, structuredFields } from './detail-data'
import { useAlertActions } from './use-alert-actions'

const INTENT: Record<AlertActionSpec['intent'], string> = {
  primary: 'border-amber-300 bg-amber-300 text-zinc-950 hover:bg-amber-200',
  neutral: 'border-zinc-700 text-zinc-200 hover:bg-zinc-800',
  danger: 'border-red-500/70 text-red-300 hover:bg-red-950/50',
}
const FOCUS = 'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300'
const CHIP = `border border-zinc-700 px-2 py-1 font-mono text-[11px] text-zinc-200 hover:bg-zinc-800 disabled:opacity-40 ${FOCUS}`
const FIELD = `border border-zinc-700 bg-zinc-950 px-2 py-1 font-mono text-xs text-zinc-100 placeholder:text-zinc-600 ${FOCUS}`
const H = 'font-mono text-[10px] uppercase tracking-widest text-zinc-500'

type Panel = null | 'snooze' | 'defer' | 'settle' | 'reply' | 'decide'

export interface AlertDetailModalProps {
  /** Override the per-kind action declaration. */
  actions?: AlertActionResolver
  mapper?: AlertMapper
}

/** Mounted by <AlertHost>; opens when `expandedId` is set on the alerts controller. */
export function AlertDetailModal(props: AlertDetailModalProps) {
  const c = useAlertsController()
  const entry = c.expandedId ? c.entries.find((e) => e.alert.id === c.expandedId) : undefined
  if (!entry) return null
  return <AlertDetail key={entry.alert.id} alert={entry.alert} status={entry.status} snoozeUntil={entry.snoozeUntil} resolution={entry.resolution} onClose={() => c.expand(null)} {...props} />
}

interface DetailProps extends AlertDetailModalProps {
  alert: Alert
  status: ItemStatus
  snoozeUntil?: number
  resolution?: string
  onClose: () => void
}

export function AlertDetail({ alert, status, snoozeUntil, resolution, onClose, actions = defaultAlertActions, mapper = defaultAlertMapper }: DetailProps) {
  const { events } = useConsoleState()
  const event = useMemo(() => events.find((e) => e.id === alert.id), [events, alert.id])
  const runner = useAlertActions(alert, event, { status })
  const shown = runner.optimistic ?? { status, snoozeUntil, resolution }
  const closed = shown.status === 'dismissed' || shown.status === 'settled'
  const handled = closed || shown.status === 'snoozed'
  const specs = useMemo(() => actions(alert, event), [actions, alert, event])
  const [panel, setPanel] = useState<Panel>(null)
  const [decide, setDecide] = useState<AlertActionSpec | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  const sev = SEVERITY_STYLE[alert.severity]
  const { markRead, now } = useAlertsController()

  useEffect(() => {
    ref.current?.focus()
    markRead(alert.id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [alert.id])

  const choose = (spec: AlertActionSpec) => {
    runner.clearMessages()
    if (spec.id === 'dismiss') void runner.run(spec).then((ok) => ok && onClose())
    else if (spec.id === 'approve' || spec.id === 'deny') {
      setDecide(spec)
      setPanel('decide')
    } else setPanel(panel === spec.id ? null : (spec.id as Panel))
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      if (panel) setPanel(null)
      else onClose()
      return
    }
    if (e.key === 'Tab') {
      const f = ref.current?.querySelectorAll<HTMLElement>('button:not([disabled]), input, select, textarea, a[href], [tabindex]:not([tabindex="-1"])')
      if (!f?.length) return
      const first = f[0]
      const last = f[f.length - 1]
      if (e.shiftKey && (document.activeElement === first || document.activeElement === ref.current)) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
      return
    }
    const t = e.target as HTMLElement
    if (e.metaKey || e.ctrlKey || e.altKey || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable || closed || runner.pending) return
    const hit = specs.find((s) => s.shortcut === e.key.toLowerCase())
    if (hit) {
      e.preventDefault()
      choose(hit)
    }
  }

  const fields = useMemo(() => structuredFields(event), [event])
  const related = useMemo(() => relatedEvents(events, event), [events, event])
  const refs = event?.subjects ?? []

  return (
    <div className="fixed inset-0 z-[60] flex items-start justify-center overflow-y-auto bg-black/60 px-3 pt-[8vh]" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={`Alert: ${alert.title}`}
        data-testid="alert-detail"
        data-status={shown.status}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        className="relative mb-8 w-full max-w-2xl border border-zinc-700 bg-zinc-950 outline-none"
      >
        <span aria-hidden="true" className={`absolute inset-y-0 left-0 w-0.5 ${sev.bar}`} />
        <header className="flex items-center gap-2 border-b border-zinc-800 py-2.5 pl-5 pr-3">
          <AlertIcon icon={alert.icon} severity={alert.severity} />
          <span className={`font-mono text-[10px] uppercase tracking-widest ${sev.text}`}>{sev.label}</span>
          <span className="font-mono text-[10px] text-zinc-500">{alert.kind}</span>
          <span className="ml-auto font-mono text-[10px] text-zinc-500">{ago(alert.ts, now)}</span>
          <button type="button" onClick={onClose} aria-label="Close alert detail" className={`h-6 w-6 text-zinc-500 hover:text-zinc-100 ${FOCUS}`}>
            <X aria-hidden="true" className="mx-auto h-3.5 w-3.5" />
          </button>
        </header>

        <section className="border-b border-zinc-800 py-3 pl-5 pr-4">
          <div className="flex items-start gap-2">
            <h2 className="min-w-0 flex-1 break-words text-sm font-medium text-zinc-50">{alert.title}</h2>
            {handled && (
              <span data-testid="alert-status" className="shrink-0 border border-zinc-700 px-1.5 py-px font-mono text-[10px] uppercase tracking-widest text-zinc-400">
                {shown.status === 'snoozed' && shown.snoozeUntil ? `snoozed until ${new Date(shown.snoozeUntil).toLocaleString([], { hour: '2-digit', minute: '2-digit', month: 'short', day: 'numeric' })}` : (shown.resolution ?? shown.status)}
              </span>
            )}
          </div>
          {alert.detail && (
            <p className="mt-1 break-words text-xs text-zinc-300">
              <Linkified text={alert.detail} />
            </p>
          )}
          <p className="mt-1 font-mono text-[11px] text-zinc-500">{alert.source}</p>
        </section>

        {fields.length > 0 && (
          <section aria-label="Details" className="border-b border-zinc-800 py-3 pl-5 pr-4">
            <h3 className={H}>Details</h3>
            <dl className="mt-2 grid grid-cols-[minmax(6rem,9rem)_1fr] gap-x-3 gap-y-1">
              {fields.map((f) => (
                <div key={f.label} className="contents">
                  <dt className="truncate font-mono text-[11px] text-zinc-500">{f.label}</dt>
                  <dd className="min-w-0 break-words font-mono text-xs text-zinc-200">
                    <Linkified text={f.value} />
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        )}

        {(refs.length > 0 || alert.primary) && (
          <section aria-label="Linked" className="border-b border-zinc-800 py-3 pl-5 pr-4">
            <h3 className={H}>Linked</h3>
            <ul className="mt-2 flex flex-wrap items-center gap-2">
              {refs.map((s) => {
                const href = safeHref(s.url)
                return (
                  <li key={`${s.type}:${s.id}`} className="flex items-center gap-1.5 font-mono text-xs text-zinc-200">
                    <span className="text-[10px] uppercase tracking-widest text-zinc-500">{s.type}</span>
                    {href ? (
                      <a href={href} className="underline decoration-zinc-600 hover:decoration-zinc-300" {...(href.startsWith('/') ? {} : { target: '_blank', rel: 'noopener noreferrer' })}>
                        {s.label ?? s.id}
                      </a>
                    ) : (
                      <Linkified text={s.label && s.label !== s.id ? `${s.label} ${s.id}` : s.id} />
                    )}
                  </li>
                )
              })}
              {alert.primary && (
                <li>
                  <AlertLinkButton link={alert.primary} onFollow={onClose} />
                </li>
              )}
            </ul>
          </section>
        )}

        <section aria-label="Timeline" className="border-b border-zinc-800 py-3 pl-5 pr-4">
          <h3 className={H}>Timeline</h3>
          <ol className="mt-2 space-y-1">
            {related.map((e) => (
              <TimelineRow key={e.id} e={e} mapper={mapper} now={now} />
            ))}
            <li className="flex gap-3 font-mono text-xs text-zinc-100">
              <span className="w-16 shrink-0 text-zinc-500">{ago(alert.ts, now)}</span>
              <span>{alert.kind} (this alert)</span>
            </li>
          </ol>
        </section>

        <ActionBar specs={specs} onPick={choose} disabled={runner.pending !== null || closed} active={panel} pending={runner.pending} />

        {panel === 'snooze' && <TimePanel title="Snooze until" presets={snoozePresets(now)} customLabel="minutes" onPick={(until) => void runner.run(specOf(specs, 'snooze'), { until }).then((ok) => ok && setPanel(null))} busy={runner.pending !== null} />}
        {panel === 'defer' && <DeferPanel onPick={(i) => void runner.run(specOf(specs, 'defer'), i).then((ok) => ok && setPanel(null))} busy={runner.pending !== null} />}
        {panel === 'settle' && <NotePanel title="Settle with note" cta="Settle" onSubmit={(note) => void runner.run(specOf(specs, 'settle'), { note }).then((ok) => ok && setPanel(null))} busy={runner.pending !== null} />}
        {panel === 'decide' && decide && (
          <NotePanel
            title={decide.id === 'approve' ? 'Approve' : 'Deny'}
            cta={decide.confirm ? `${decide.label} (confirm identity)` : decide.label}
            optional
            intent={decide.intent}
            onSubmit={(note) => void runner.run(decide, { note }).then((ok) => ok && setPanel(null))}
            busy={runner.pending !== null}
          />
        )}
        {panel === 'reply' && <ReplyPanel key={alert.id} alert={alert} event={event} onDone={() => setPanel(null)} />}

        <div className="min-h-[2rem] border-t border-zinc-800 py-1.5 pl-5 pr-4 font-mono text-[11px]">
          {runner.error ? (
            <span role="alert" className="text-red-300">{runner.error}</span>
          ) : runner.notice ? (
            <span role="status" className="text-emerald-300">{runner.notice}</span>
          ) : (
            <span className="text-zinc-600">{specs.map((s) => `${s.shortcut.toUpperCase()} ${s.label.toLowerCase()}`).join(' · ')} · Esc close</span>
          )}
          {closed && (
            <button type="button" onClick={() => void runner.restore()} className={`ml-3 border border-zinc-700 px-1.5 py-px uppercase tracking-widest text-zinc-300 hover:bg-zinc-800 ${FOCUS}`}>
              Restore
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

const specOf = (specs: AlertActionSpec[], id: AlertActionId): AlertActionSpec => specs.find((s) => s.id === id) ?? { id, label: id, intent: 'neutral', shortcut: '' }

function TimelineRow({ e, mapper, now }: { e: ConsoleEvent; mapper: AlertMapper; now: number }) {
  const m = mapper(e)
  return (
    <li className="flex gap-3 font-mono text-xs text-zinc-400">
      <span className="w-16 shrink-0 text-zinc-500">{ago(e.ts, now)}</span>
      <span className="min-w-0 break-words">{m?.title ?? e.kind}</span>
    </li>
  )
}

function ActionBar({ specs, onPick, disabled, active, pending }: { specs: AlertActionSpec[]; onPick: (s: AlertActionSpec) => void; disabled: boolean; active: Panel; pending: string | null }) {
  return (
    <div role="toolbar" aria-label="Alert actions" className="flex flex-wrap items-center gap-2 py-3 pl-5 pr-4">
      {specs.map((s) => (
        <button
          key={s.id}
          type="button"
          disabled={disabled}
          aria-pressed={active === s.id}
          aria-keyshortcuts={s.shortcut}
          onClick={() => onPick(s)}
          className={`border px-2.5 py-1 font-mono text-[11px] uppercase tracking-wider transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${FOCUS} ${INTENT[s.intent]}`}
        >
          {pending === s.id ? `${s.label}...` : s.label}
          {s.confirm && <span aria-label="requires identity confirmation" className="ml-1 opacity-70">*</span>}
        </button>
      ))}
    </div>
  )
}

function PanelShell({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section aria-label={title} className="border-t border-zinc-800 bg-zinc-900/40 py-3 pl-5 pr-4">
      <h3 className={H}>{title}</h3>
      <div className="mt-2">{children}</div>
    </section>
  )
}

function TimePanel({ title, presets, onPick, busy, customLabel }: { title: string; presets: { id: string; label: string; until: number }[]; onPick: (until: number) => void; busy: boolean; customLabel: string }) {
  const [custom, setCustom] = useState('')
  const mins = Number(custom)
  return (
    <PanelShell title={title}>
      <div className="flex flex-wrap items-center gap-2">
        {presets.map((p) => (
          <button key={p.id} type="button" disabled={busy} className={CHIP} onClick={() => onPick(p.until)}>
            {p.label}
          </button>
        ))}
        <label className="flex items-center gap-1.5 font-mono text-[11px] text-zinc-400">
          Custom
          <input type="number" min={1} value={custom} onChange={(e) => setCustom(e.target.value)} aria-label={`Custom ${customLabel}`} className={`${FIELD} w-20`} placeholder={customLabel} />
          <button type="button" disabled={busy || !(mins > 0)} className={CHIP} onClick={() => onPick(Date.now() + mins * 60_000)}>
            Set
          </button>
        </label>
      </div>
    </PanelShell>
  )
}

function DeferPanel({ onPick, busy }: { onPick: (i: { until?: number; context?: string }) => void; busy: boolean }) {
  const [mode, setMode] = useState<'time' | 'context'>('time')
  return (
    <PanelShell title="Defer">
      <div role="tablist" aria-label="Defer to" className="mb-2 flex gap-4 font-mono text-[10px] uppercase tracking-widest">
        {(['time', 'context'] as const).map((m) => (
          <button key={m} role="tab" type="button" aria-selected={mode === m} onClick={() => setMode(m)} className={`border-b-2 pb-0.5 ${FOCUS} ${mode === m ? 'border-amber-300 text-zinc-100' : 'border-transparent text-zinc-500 hover:text-zinc-300'}`}>
            To a {m}
          </button>
        ))}
      </div>
      {mode === 'time' ? (
        <TimePanelBody onPick={(until) => onPick({ until })} busy={busy} />
      ) : (
        <div className="flex flex-wrap gap-2">
          {DEFER_CONTEXTS.map((c) => (
            <button key={c.id} type="button" disabled={busy} className={CHIP} onClick={() => onPick({ context: c.label })}>
              {c.label}
            </button>
          ))}
        </div>
      )}
    </PanelShell>
  )
}

function TimePanelBody({ onPick, busy }: { onPick: (until: number) => void; busy: boolean }) {
  const [mountedAt] = useState(() => Date.now())
  const [at, setAt] = useState('')
  const when = at ? new Date(at).getTime() : NaN
  return (
    <div className="flex flex-wrap items-center gap-2">
      {deferPresets(mountedAt).map((p) => (
        <button key={p.id} type="button" disabled={busy} className={CHIP} onClick={() => onPick(p.until)}>
          {p.label}
        </button>
      ))}
      <input type="datetime-local" aria-label="Defer until" value={at} onChange={(e) => setAt(e.target.value)} className={FIELD} />
      <button type="button" disabled={busy || !(when > mountedAt)} className={CHIP} onClick={() => onPick(when)}>
        Set
      </button>
    </div>
  )
}

function NotePanel({ title, cta, onSubmit, busy, optional = true, intent = 'primary' }: { title: string; cta: string; onSubmit: (note: string | undefined) => void; busy: boolean; optional?: boolean; intent?: AlertActionSpec['intent'] }) {
  const [note, setNote] = useState('')
  const ref = useRef<HTMLTextAreaElement>(null)
  useEffect(() => ref.current?.focus(), [])
  return (
    <PanelShell title={title}>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          onSubmit(note.trim() || undefined)
        }}
        className="flex flex-col gap-2"
      >
        <textarea ref={ref} value={note} onChange={(e) => setNote(e.target.value)} rows={2} maxLength={2000} aria-label="Note" placeholder={optional ? 'Note (optional)' : 'Note'} className={`${FIELD} w-full resize-y`} />
        <div>
          <button type="submit" disabled={busy} className={`border px-3 py-1.5 font-mono text-[11px] uppercase tracking-widest disabled:opacity-40 ${FOCUS} ${INTENT[intent]}`}>
            {busy ? 'Working...' : cta}
          </button>
        </div>
      </form>
    </PanelShell>
  )
}

/** Opens the M1 composer in-thread. The recipient is the agent / sender the alert came from. */
function ReplyPanel({ alert, event, onDone }: { alert: Alert; event?: ConsoleEvent; onDone: () => void }) {
  const { client } = useMessaging()
  const { markRead } = useAlertsController()
  const ref = replyTarget(alert, event)
  const [recipient, setRecipient] = useState<Recipient | null | undefined>(ref ? undefined : null)
  useEffect(() => {
    let live = true
    if (!ref) return
    client.resolveRecipient(ref).then(
      (r) => live && setRecipient(r),
      () => live && setRecipient(null),
    )
    return () => {
      live = false
    }
  }, [client, ref])
  return (
    <PanelShell title="Reply">
      {recipient === undefined ? (
        <p className="font-mono text-xs text-zinc-500">Resolving recipient...</p>
      ) : recipient === null ? (
        <p role="alert" className="font-mono text-xs text-red-300">No messageable sender for this alert.</p>
      ) : (
        <MessageComposer
          recipient={recipient}
          replyTo={{ id: alert.id, threadId: alert.threadId }}
          subject={alert.title}
          onSent={() => {
            markRead(alert.id)
            onDone()
          }}
          onCancel={onDone}
        />
      )}
    </PanelShell>
  )
}
