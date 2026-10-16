import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useAgentsClient } from './context'
import { Field, inputCls, ListField } from './AgentEditor'
import { blankDraft, computeLifecyclePlan, draftFromClone, draftFromTemplate, slugify, type AgentTemplate } from './lifecycle'
import { KNOWN_RULES, KNOWN_SCOPES, KNOWN_TOOLS, PERMISSION_MODES, PROVIDERS, SANDBOX_MODES, type AgentDraft, type AgentPlan } from './plan'
import { PlanRunner } from './PlanRunner'
import { Block, Btn, Chip, RiskBadge } from './ui'
import type { AgentRecord } from './types'

type Step = 'source' | 'identity' | 'policy' | 'reason'
const STEPS: { id: Step; label: string }[] = [
  { id: 'source', label: 'Start from' },
  { id: 'identity', label: 'Identity and runtime' },
  { id: 'policy', label: 'Tools and policy' },
  { id: 'reason', label: 'Reason and review' },
]

type Source = { kind: 'scratch' } | { kind: 'template'; id: string } | { kind: 'clone'; id: string }

function Choice({ selected, onSelect, title, children }: { selected: boolean; onSelect: () => void; title: string; children?: ReactNode }) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={`w-full border px-3 py-2.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 ${selected ? 'border-amber-300/70 bg-zinc-900' : 'border-zinc-800 hover:bg-zinc-900/50'}`}
    >
      <span className="block font-mono text-[13px] text-zinc-100">{title}</span>
      {children && <span className="mt-0.5 block text-[12px] text-zinc-400">{children}</span>}
    </button>
  )
}

export interface CreateWizardProps {
  agents: AgentRecord[]
  /** Open with this agent preselected as the clone source. */
  cloneOf?: string
  onClose: () => void
  /** Called once the new agent is created. */
  onCreated: (agent: AgentRecord) => void
}

/** Create an agent from scratch, a template, or an existing agent. The result is a plan, never a direct write. */
export function CreateWizard({ agents, cloneOf, onClose, onCreated }: CreateWizardProps) {
  const client = useAgentsClient()
  const [templates, setTemplates] = useState<AgentTemplate[]>([])
  const [step, setStep] = useState<Step>('source')
  const [source, setSource] = useState<Source>(cloneOf ? { kind: 'clone', id: cloneOf } : { kind: 'scratch' })
  const [draft, setDraft] = useState<AgentDraft>(() => {
    const a = cloneOf ? agents.find((x) => x.identity.id === cloneOf) : undefined
    return a ? draftFromClone(a) : blankDraft()
  })
  const [name, setName] = useState(() => {
    const a = cloneOf ? agents.find((x) => x.identity.id === cloneOf) : undefined
    return a ? `${a.identity.name} copy` : ''
  })
  const [id, setId] = useState('')
  const [idTouched, setIdTouched] = useState(false)
  const [note, setNote] = useState('')
  const [plan, setPlan] = useState<AgentPlan | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let active = true
    client.listTemplates().then((t) => active && setTemplates(t), () => undefined)
    return () => {
      active = false
    }
  }, [client])

  const taken = useMemo(() => new Set(agents.map((a) => a.identity.id)), [agents])
  const effectiveId = idTouched ? id : slugify(name)
  const set = <K extends keyof AgentDraft>(k: K, v: AgentDraft[K]) => setDraft((d) => ({ ...d, [k]: v }))

  const pick = (s: Source) => {
    setSource(s)
    if (s.kind === 'scratch') { setDraft(blankDraft()); return }
    if (s.kind === 'template') {
      const t = templates.find((x) => x.id === s.id)
      if (t) setDraft(draftFromTemplate(t))
      return
    }
    const a = agents.find((x) => x.identity.id === s.id)
    if (a) {
      setDraft(draftFromClone(a))
      if (!name) setName(`${a.identity.name} copy`)
    }
  }

  // The same pure planner the server mirrors, run locally for instant feedback.
  const live = useMemo(
    () =>
      computeLifecyclePlan(
        { op: 'agent.create', agentId: effectiveId, name, draft, note, cloneOf: source.kind === 'clone' ? source.id : undefined, templateId: source.kind === 'template' ? source.id : undefined },
        { agents, teams: [], assignments: [] },
        { id: 'preview', now: 0, actor: { id: 'preview' } },
      ),
    [agents, effectiveId, name, draft, note, source],
  )
  const idIssue = live.issues.find((i) => i.field === 'lifecycle' && /Id must|already exists/.test(i.message))
  const nameIssue = !name.trim()
  const identityOk = !idIssue && !nameIssue && !!draft.provider.trim()
  const hardErrors = live.issues.filter((i) => i.level === 'error')

  const review = async () => {
    setBusy(true)
    setError(null)
    try {
      setPlan(await client.planLifecycle({ op: 'agent.create', agentId: effectiveId, name: name.trim(), draft, note, cloneOf: source.kind === 'clone' ? source.id : undefined, templateId: source.kind === 'template' ? source.id : undefined }))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not plan')
    } finally {
      setBusy(false)
    }
  }

  if (plan) {
    return (
      <article aria-label="Create agent plan">
        <header className="border-b border-zinc-800/80 px-5 py-3"><h2 className="font-mono text-sm font-semibold uppercase tracking-wide">Create {name.trim() || effectiveId}</h2></header>
        <PlanRunner
          plan={plan}
          subjectLabel={name.trim() || effectiveId}
          onBack={() => setPlan(null)}
          onDone={onClose}
          onChanged={(o) => o.status === 'applied' && o.agent && onCreated(o.agent)}
        />
      </article>
    )
  }

  const at = STEPS.findIndex((s) => s.id === step)
  return (
    <article aria-label="Create agent">
      <header className="flex flex-wrap items-center gap-3 border-b border-zinc-800/80 px-5 py-3">
        <h2 className="font-mono text-sm font-semibold uppercase tracking-wide">New agent</h2>
        <ol aria-label="Steps" className="flex flex-wrap items-center gap-x-3 font-mono text-[10px] uppercase tracking-widest">
          {STEPS.map((s, n) => (
            <li key={s.id} aria-current={s.id === step ? 'step' : undefined} className={s.id === step ? 'text-amber-300' : n < at ? 'text-zinc-300' : 'text-zinc-600'}>{n + 1}. {s.label}</li>
          ))}
        </ol>
        <span className="ml-auto flex items-center gap-2">{step !== 'source' && <RiskBadge risk={live.risk} />}<Btn onClick={onClose}>Cancel</Btn></span>
      </header>

      {step === 'source' && (
        <>
          <Block title="Start from">
            <div role="radiogroup" aria-label="Start from" className="space-y-1.5">
              <Choice selected={source.kind === 'scratch'} onSelect={() => pick({ kind: 'scratch' })} title="From scratch">Safe defaults: asks before risky tools, workspace sandbox, no network, no privileged scopes.</Choice>
              <Choice selected={source.kind === 'template'} onSelect={() => pick(templates[0] ? { kind: 'template', id: templates[0].id } : source)} title="From a template">A curated starting point for a common role.</Choice>
              <Choice selected={source.kind === 'clone'} onSelect={() => pick({ kind: 'clone', id: agents.find((a) => !a.identity.retired)?.identity.id ?? '' })} title="Clone an existing agent">Copies runtime, tools, skills and policy, including any privileges.</Choice>
            </div>
          </Block>
          {source.kind === 'template' && (
            <Block title="Template">
              <div role="radiogroup" aria-label="Template" className="grid gap-1.5 sm:grid-cols-2">
                {templates.map((t) => <Choice key={t.id} selected={source.id === t.id} onSelect={() => pick({ kind: 'template', id: t.id })} title={t.name}>{t.summary}</Choice>)}
              </div>
            </Block>
          )}
          {source.kind === 'clone' && (
            <Block title="Clone from">
              <select aria-label="Agent to clone" value={source.id} onChange={(e) => pick({ kind: 'clone', id: e.target.value })} className={`${inputCls} max-w-sm`}>
                {agents.filter((a) => !a.identity.retired).map((a) => <option key={a.identity.id} value={a.identity.id}>{a.identity.name} ({a.identity.id})</option>)}
              </select>
              <p className="mt-2 text-[12px] text-zinc-500">The clone gets a new identity: no teams, assignments, sessions or message address.</p>
            </Block>
          )}
        </>
      )}

      {step === 'identity' && (
        <Block title="Identity and runtime">
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Name" htmlFor="cw-name" issues={nameIssue ? [{ level: 'error', field: 'lifecycle', message: 'Name is required.' }] : undefined}>
              <input id="cw-name" value={name} onChange={(e) => setName(e.target.value)} className={inputCls} />
            </Field>
            <Field label="Id" htmlFor="cw-id" issues={idIssue ? [idIssue] : undefined}>
              <input id="cw-id" value={effectiveId} onChange={(e) => { setId(e.target.value); setIdTouched(true) }} className={inputCls} />
            </Field>
            <div className="sm:col-span-2"><Field label="Summary" htmlFor="cw-summary"><input id="cw-summary" value={draft.summary} onChange={(e) => set('summary', e.target.value)} className={inputCls} /></Field></div>
            <div className="sm:col-span-2"><Field label="Roles"><ListField label="Roles" values={draft.roles} suggestions={['worker', 'reviewer', 'coordinator', 'partner']} onChange={(v) => set('roles', v)} /></Field></div>
            <Field label="Provider" htmlFor="cw-provider">
              <select id="cw-provider" value={draft.provider} onChange={(e) => set('provider', e.target.value)} className={inputCls}>{[...new Set([...PROVIDERS, draft.provider])].map((p) => <option key={p}>{p}</option>)}</select>
            </Field>
            <Field label="Model" htmlFor="cw-model"><input id="cw-model" value={draft.model} onChange={(e) => set('model', e.target.value)} className={inputCls} /></Field>
            <Field label="Project" htmlFor="cw-project"><input id="cw-project" value={draft.project} onChange={(e) => set('project', e.target.value)} className={inputCls} /></Field>
            <Field label="Worker" htmlFor="cw-worker"><input id="cw-worker" value={draft.worker} onChange={(e) => set('worker', e.target.value)} className={inputCls} /></Field>
            <Field label="Workspace" htmlFor="cw-ws">
              <select id="cw-ws" value={draft.workspaceMode} onChange={(e) => set('workspaceMode', e.target.value)} className={inputCls}>{[...new Set(['', 'worktree', 'hybrid', 'direct', draft.workspaceMode])].map((p) => <option key={p} value={p}>{p || 'not set'}</option>)}</select>
            </Field>
            <Field label="Enabled" htmlFor="cw-enabled"><label className="flex items-center gap-2 text-[13px] text-zinc-200"><input id="cw-enabled" type="checkbox" checked={draft.enabled} onChange={(e) => set('enabled', e.target.checked)} /> Can be launched once created</label></Field>
          </div>
        </Block>
      )}

      {step === 'policy' && (
        <>
          <Block title="Tools and skills">
            <div className="space-y-3">
              <Field label="Tool allowlist"><ListField label="Tools" values={draft.tools} suggestions={KNOWN_TOOLS} tone="border-emerald-400/50 bg-emerald-400/10 text-emerald-300" onChange={(v) => set('tools', v)} /></Field>
              <Field label="Skills"><ListField label="Skills" values={draft.skills} suggestions={[]} onChange={(v) => set('skills', v)} /></Field>
              <Field label="Boot profile" htmlFor="cw-boot"><input id="cw-boot" value={draft.bootProfile} onChange={(e) => set('bootProfile', e.target.value)} className={`${inputCls} max-w-xs`} /></Field>
              <Field label={`Prompt (${draft.prompt.length}/8000)`} htmlFor="cw-prompt"><textarea id="cw-prompt" value={draft.prompt} onChange={(e) => set('prompt', e.target.value)} rows={3} className={`${inputCls} h-auto py-1.5`} /></Field>
            </div>
          </Block>
          <Block title="Permissions and policy">
            <div className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="Permission mode" htmlFor="cw-perm"><select id="cw-perm" value={draft.permissionMode} onChange={(e) => set('permissionMode', e.target.value)} className={inputCls}>{PERMISSION_MODES.map((p) => <option key={p}>{p}</option>)}</select></Field>
                <Field label="Sandbox" htmlFor="cw-sandbox"><select id="cw-sandbox" value={draft.sandbox} onChange={(e) => set('sandbox', e.target.value)} className={inputCls}>{[...new Set(['', ...SANDBOX_MODES])].map((p) => <option key={p} value={p}>{p || 'not set'}</option>)}</select></Field>
              </div>
              <Field label="Network" htmlFor="cw-net"><label className="flex items-center gap-2 text-[13px] text-zinc-200"><input id="cw-net" type="checkbox" checked={draft.network} onChange={(e) => set('network', e.target.checked)} /> Allow network access</label></Field>
              <Field label="Privileged scopes"><ListField label="Scopes" values={draft.scopes} suggestions={KNOWN_SCOPES} tone="border-amber-400/60 bg-amber-400/10 text-amber-300" onChange={(v) => set('scopes', v)} /></Field>
              <Field label="Policy rules"><ListField label="Rules" values={draft.rules} suggestions={KNOWN_RULES} onChange={(v) => set('rules', v)} /></Field>
            </div>
          </Block>
        </>
      )}

      {step === 'reason' && (
        <>
          <Block title="Summary">
            <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 text-[13px]">
              <span className="font-mono text-zinc-100">{name.trim() || '(no name)'}</span>
              <span className="font-mono text-zinc-500">{effectiveId}</span>
              <span className="font-mono text-zinc-400">{draft.provider}{draft.model ? ` · ${draft.model}` : ''}</span>
              <span className="text-zinc-500">{source.kind === 'scratch' ? 'from scratch' : source.kind === 'template' ? `template ${source.id}` : `clone of ${source.id}`}</span>
            </div>
            {live.risks.length > 0 ? (
              <ul className="mt-3 space-y-1">
                {live.risks.map((r) => <li key={r.id} className="flex flex-wrap items-baseline gap-x-3 text-[13px]"><Chip tone={r.level === 'high' ? 'border-rose-400/60 text-rose-300' : r.level === 'medium' ? 'border-amber-400/50 text-amber-300' : undefined}>{r.level}</Chip><span className="text-zinc-100">{r.title}</span><span className="text-zinc-500">{r.detail}</span></li>)}
              </ul>
            ) : <p className="mt-3 text-[13px] text-zinc-400">No privileged settings.</p>}
          </Block>
          <Block title="Reason">
            <Field label={live.risk === 'high' ? 'Reason (required: privileged agent)' : 'Reason (optional)'} htmlFor="cw-note" issues={live.issues.filter((i) => i.field === 'note')}>
              <input id="cw-note" value={note} onChange={(e) => setNote(e.target.value)} className={inputCls} placeholder="Why this agent exists" />
            </Field>
          </Block>
          {hardErrors.filter((i) => i.field !== 'note').length > 0 && (
            <ul className="border-b border-rose-400/30 bg-rose-400/5 px-5 py-2 text-[13px] text-rose-200">{hardErrors.filter((i) => i.field !== 'note').map((i, n) => <li key={n}>{i.message}</li>)}</ul>
          )}
        </>
      )}
      {error && <div role="alert" className="border-b border-rose-400/30 bg-rose-400/5 px-5 py-2 text-[13px] text-rose-200">{error}</div>}

      <div className="sticky bottom-0 flex flex-wrap items-center gap-2 border-t border-zinc-800 bg-zinc-950 px-5 py-3">
        {at > 0 && <Btn onClick={() => setStep(STEPS[at - 1].id)} disabled={busy}>Back</Btn>}
        <span className="ml-auto" />
        {step !== 'reason' ? (
          <Btn tone="primary" onClick={() => setStep(STEPS[at + 1].id)} disabled={(step === 'source' && source.kind === 'clone' && !taken.has(source.id)) || (step === 'identity' && !identityOk)}>Next</Btn>
        ) : (
          <Btn tone="primary" onClick={review} disabled={busy || hardErrors.length > 0}>Review plan</Btn>
        )}
      </div>
    </article>
  )
}
