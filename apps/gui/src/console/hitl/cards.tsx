// The four HITL card kinds. Each renders compact (tray/toast row), inline
// (one line) and expanded (interactive). Nothing here auto-approves: every
// decision is an explicit click, approve runs through ActionBus + IdentityConfirm.
import { useId, useState } from 'react'
import { CardFrame } from '../cards/frame'
import type { CardRenderProps, CardActionSpec } from '../cards/types'
import { secretHandoff } from './client'
import { approvalActions, HITL_ACTION } from './model'
import { AuditTrail, Btn, ClosedNote, ContextLinks, Countdown, FIELD, MetaLine, StatusLine } from './parts'
import { useHitlCard, type HitlCardState } from './use-hitl-card'
import { hasValue } from './client'
import type { HitlApprovalRequest, HitlField, HitlFormRequest, HitlFormValues, HitlQuestionRequest, HitlSecretRequest } from './types'

const LABEL = { approval: 'Approval', question: 'Question', secret: 'Secret', form: 'Form' } as const

function Shell({ p, s, children }: { p: CardRenderProps; s: HitlCardState; children?: React.ReactNode }) {
  if (p.density === 'inline') {
    return (
      <span data-card-kind={p.card.kind} data-card-phase={p.card.phase} className="inline-flex items-center gap-2 font-mono text-[11px] text-zinc-300">
        <span aria-hidden className="h-1.5 w-1.5 bg-amber-400" />
        {LABEL[s.req.type]}: {p.card.title}
        <Countdown remaining={s.req.status === 'pending' ? s.remaining : undefined} />
      </span>
    )
  }
  const compact = p.density === 'compact'
  return (
    <CardFrame card={p.card} density={p.density} kindLabel={LABEL[s.req.type]} onExpand={p.onExpand} footer={compact ? undefined : <AuditTrail audit={s.req.audit} />}>
      <MetaLine req={s.req} remaining={s.remaining} />
      {!compact && <ContextLinks links={s.req.contextLinks} />}
      {!compact && children}
      {!compact && <ClosedNote s={s} />}
    </CardFrame>
  )
}

function Unreadable({ p }: { p: CardRenderProps }) {
  return (
    <CardFrame card={p.card} density={p.density === 'inline' ? 'compact' : p.density} kindLabel="HITL" onExpand={p.onExpand}>
      <p className="mt-1 font-mono text-[11px] text-red-300">Request payload could not be read.</p>
    </CardFrame>
  )
}

/* ----------------------------- approval ----------------------------- */

export function HitlApprovalCard(p: CardRenderProps) {
  const s = useHitlCard(p.card)
  const [reason, setReason] = useState('')
  const rid = useId()
  if (!s || s.req.type !== 'approval') return <Unreadable p={p} />
  const req: HitlApprovalRequest = s.req
  const run = (a: CardActionSpec) => void p.controller.run({ ...a, params: { ...a.params, reason } })
  const busy = Object.values(p.controller.status).some((x) => x.pending)
  return (
    <Shell p={p} s={s}>
      {req.effects && req.effects.length > 0 && (
        <ul className="mt-2 space-y-0.5 border-l border-zinc-700 pl-3">
          {req.effects.map((e, i) => <li key={i} className="font-mono text-[11px] text-zinc-300">{e}</li>)}
        </ul>
      )}
      {req.scope.length > 0 && <p className="mt-2 font-mono text-[11px] text-zinc-500">scope: <span className="text-zinc-300">{req.scope.join(', ')}</span></p>}
      {!s.locked && (
        <div className="mt-3">
          <label htmlFor={rid} className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">Note (optional, recorded in the audit)</label>
          <textarea id={rid} rows={2} value={reason} onChange={(e) => setReason(e.target.value)} disabled={busy} className={`${FIELD} mt-1`} />
          <div className="flex flex-wrap items-center gap-2 pt-2">
            {approvalActions(req).map((a) => (
              <Btn key={a.id} intent={a.intent} disabled={busy} onClick={() => run(a)}>
                {p.controller.status[a.id]?.pending ? `${a.label}...` : a.label}
                {a.confirm ? <span aria-label="requires identity confirmation" className="ml-1 opacity-70">*</span> : null}
              </Btn>
            ))}
            <StatusLine controller={p.controller} ids={['approve', 'reject', 'defer']} />
          </div>
          <p className="pt-1.5 font-mono text-[10px] text-zinc-500">* approving asks you to confirm your identity first</p>
        </div>
      )}
    </Shell>
  )
}

/* ----------------------------- question ----------------------------- */

export function HitlQuestionCard(p: CardRenderProps) {
  const s = useHitlCard(p.card)
  const [picked, setPicked] = useState<string[]>([])
  const [other, setOther] = useState('')
  const [text, setText] = useState('')
  const gid = useId()
  if (!s || s.req.type !== 'question') return <Unreadable p={p} />
  const req: HitlQuestionRequest = s.req
  const busy = Object.values(p.controller.status).some((x) => x.pending)
  const answer: string | string[] =
    req.mode === 'text' ? text.trim() : req.mode === 'multi' ? [...picked, ...(other.trim() ? [other.trim()] : [])] : other.trim() ? other.trim() : (picked[0] ?? '')
  const ready = Array.isArray(answer) ? answer.length > 0 : answer !== ''
  const submit = () => void p.controller.run({ id: 'answer', label: 'Send answer', intent: 'primary', kind: HITL_ACTION.answer, params: { requestId: req.id, answer }, origin: 'declared' })
  return (
    <Shell p={p} s={s}>
      {!s.locked && (
        <fieldset className="mt-3" disabled={busy}>
          {req.mode === 'text' ? (
            <textarea aria-label="Answer" rows={4} value={text} placeholder={req.placeholder ?? 'Type your answer'} onChange={(e) => setText(e.target.value)} className={FIELD} />
          ) : (
            <>
              <legend className="sr-only">Options</legend>
              <div className="border border-zinc-800">
                {req.options.map((o) => {
                  const on = picked.includes(o.value)
                  return (
                    <label key={o.value} className={`flex cursor-pointer items-start gap-2.5 border-b border-zinc-800 px-3 py-2 last:border-b-0 hover:bg-zinc-900 ${on ? 'bg-zinc-900' : ''}`}>
                      <input
                        type={req.mode === 'multi' ? 'checkbox' : 'radio'}
                        name={gid}
                        checked={on}
                        onChange={() => {
                          if (req.mode === 'multi') setPicked(on ? picked.filter((x) => x !== o.value) : [...picked, o.value])
                          else {
                            setPicked([o.value])
                            setOther('')
                          }
                        }}
                        className="mt-1 accent-amber-300"
                      />
                      <span>
                        <span className="block text-sm text-zinc-100">{o.label}</span>
                        {o.description && <span className="block text-xs text-zinc-400">{o.description}</span>}
                      </span>
                    </label>
                  )
                })}
              </div>
              {req.allowOther && (
                <input aria-label="Other answer" value={other} placeholder="Other..." onChange={(e) => { setOther(e.target.value); if (req.mode === 'choice') setPicked([]) }} className={`${FIELD} mt-2`} />
              )}
            </>
          )}
          <div className="flex items-center gap-2 pt-3">
            <Btn intent="primary" disabled={!ready || busy} onClick={submit}>{p.controller.status.answer?.pending ? 'Sending...' : 'Send answer'}</Btn>
            <StatusLine controller={p.controller} ids={['answer']} />
          </div>
        </fieldset>
      )}
    </Shell>
  )
}

/* ------------------------------ secret ------------------------------ */

/**
 * The typed value lives only in this component's local state until submit. It is
 * handed to the SecretHandoff (never to ActionBus params, the StateStore, logs or
 * the controller status), the input is cleared immediately, and the handle is
 * always discarded afterwards. There is deliberately no reveal toggle.
 */
export function HitlSecretCard(p: CardRenderProps) {
  const s = useHitlCard(p.card)
  const [value, setValue] = useState('')
  const iid = useId()
  if (!s || s.req.type !== 'secret') return <Unreadable p={p} />
  const req: HitlSecretRequest = s.req
  const busy = Object.values(p.controller.status).some((x) => x.pending)
  const submit = async () => {
    const handle = secretHandoff.put(value)
    setValue('')
    try {
      await p.controller.run({ id: 'secret', label: 'Send secret', intent: 'primary', kind: HITL_ACTION.secret, params: { requestId: req.id, handle }, origin: 'declared' })
    } finally {
      secretHandoff.discard(handle)
    }
  }
  return (
    <Shell p={p} s={s}>
      <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 font-mono text-[11px]">
        <dt className="text-zinc-500">purpose</dt><dd className="text-zinc-300">{req.purpose}</dd>
        {req.destination && (<><dt className="text-zinc-500">delivered to</dt><dd className="text-zinc-300">{req.destination}</dd></>)}
      </dl>
      {!s.locked && (
        <form className="mt-3" onSubmit={(e) => { e.preventDefault(); if (value && !busy) void submit() }} autoComplete="off">
          <label htmlFor={iid} className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">{req.fieldLabel}</label>
          <input id={iid} type="password" autoComplete="off" spellCheck={false} data-lpignore="true" value={value} onChange={(e) => setValue(e.target.value)} disabled={busy} className={`${FIELD} mt-1 font-mono`} />
          <div className="flex flex-wrap items-center gap-2 pt-2">
            <button type="submit" disabled={!value || busy} className="border border-amber-300 bg-amber-300 px-2.5 py-1 font-mono text-[11px] uppercase tracking-wider text-zinc-950 hover:bg-amber-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40">{busy ? 'Sending...' : 'Send secret'}</button>
            <StatusLine controller={p.controller} ids={['secret']} />
          </div>
          <p className="pt-1.5 font-mono text-[10px] text-zinc-500">Sent once to the destination. Not stored, logged or shown again by the Console.</p>
        </form>
      )}
    </Shell>
  )
}

/* ------------------------------- form ------------------------------- */

function initial(fields: HitlField[]): HitlFormValues {
  const v: HitlFormValues = {}
  for (const f of fields) {
    if (f.default !== undefined) v[f.id] = f.default
    else if (f.kind === 'checkbox') v[f.id] = false
  }
  return v
}

function FieldInput({ f, value, onChange, disabled, error }: { f: HitlField; value: HitlFormValues[string] | undefined; onChange: (v: HitlFormValues[string]) => void; disabled: boolean; error?: string }) {
  const id = useId()
  const common = { id, disabled, 'aria-invalid': !!error || undefined, 'aria-describedby': error ? `${id}-e` : undefined }
  return (
    <div>
      {f.kind === 'checkbox' ? (
        <label className="flex items-center gap-2 text-sm text-zinc-100">
          <input type="checkbox" {...common} checked={value === true} onChange={(e) => onChange(e.target.checked)} className="accent-amber-300" />
          {f.label}{f.required ? ' *' : ''}
        </label>
      ) : (
        <>
          <label htmlFor={id} className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">{f.label}{f.required ? ' *' : ''}</label>
          {f.kind === 'textarea' ? (
            <textarea {...common} rows={3} value={typeof value === 'string' ? value : ''} placeholder={f.placeholder} onChange={(e) => onChange(e.target.value)} className={`${FIELD} mt-1`} />
          ) : f.kind === 'select' ? (
            <select {...common} value={typeof value === 'string' ? value : ''} onChange={(e) => onChange(e.target.value)} className={`${FIELD} mt-1`}>
              <option value="">Select...</option>
              {(f.options ?? []).map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
          ) : f.kind === 'number' ? (
            <input {...common} type="number" value={typeof value === 'number' ? value : ''} placeholder={f.placeholder} onChange={(e) => onChange(e.target.value === '' ? '' : Number(e.target.value))} className={`${FIELD} mt-1`} />
          ) : (
            <input {...common} type="text" value={typeof value === 'string' ? value : ''} placeholder={f.placeholder} onChange={(e) => onChange(e.target.value)} className={`${FIELD} mt-1`} />
          )}
        </>
      )}
      {f.help && !error && <p className="mt-0.5 text-xs text-zinc-500">{f.help}</p>}
      {error && <p id={`${id}-e`} role="alert" className="mt-0.5 font-mono text-[11px] text-red-300">{error}</p>}
    </div>
  )
}

export function HitlFormCard(p: CardRenderProps) {
  const s = useHitlCard(p.card)
  const [values, setValues] = useState<HitlFormValues | null>(null)
  const [errors, setErrors] = useState<Record<string, string>>({})
  if (!s || s.req.type !== 'form') return <Unreadable p={p} />
  const req: HitlFormRequest = s.req
  const cur = values ?? initial(req.fields)
  const busy = Object.values(p.controller.status).some((x) => x.pending)
  const submit = () => {
    const errs: Record<string, string> = {}
    for (const f of req.fields) if (f.required && !hasValue(cur[f.id], f.kind)) errs[f.id] = 'Required'
    setErrors(errs)
    if (Object.keys(errs).length) return
    void p.controller.run({ id: 'form', label: 'Submit', intent: 'primary', kind: HITL_ACTION.form, params: { requestId: req.id, values: cur }, origin: 'declared' })
  }
  return (
    <Shell p={p} s={s}>
      {!s.locked && (
        <form className="mt-3 space-y-3" noValidate onSubmit={(e) => { e.preventDefault(); if (!busy) submit() }}>
          {req.fields.map((f) => (
            <FieldInput key={f.id} f={f} value={cur[f.id]} disabled={busy} error={errors[f.id]} onChange={(v) => { setValues({ ...cur, [f.id]: v }); if (errors[f.id]) setErrors({ ...errors, [f.id]: '' }) }} />
          ))}
          <div className="flex items-center gap-2 pt-1">
            <button type="submit" disabled={busy} className="border border-amber-300 bg-amber-300 px-2.5 py-1 font-mono text-[11px] uppercase tracking-wider text-zinc-950 hover:bg-amber-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40">{busy ? 'Submitting...' : (req.submitLabel ?? 'Submit')}</button>
            <StatusLine controller={p.controller} ids={['form']} />
          </div>
        </form>
      )}
    </Shell>
  )
}
