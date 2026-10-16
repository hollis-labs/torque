import { useMemo, useState, type ReactNode } from 'react'
import { useAgentsClient } from './context'
import { AGENT_ACTIONS, agentPlanAction, type AgentActionKind } from './actions'
import { useAgentEdit } from './edit-context'
import { PlanReview } from './PlanReview'
import { StepUpHost } from './StepUpDialog'
import { Block, Btn, Chip, RiskBadge } from './ui'
import {
  assessRisks, diffDraft, draftFromAgent, KNOWN_RULES, KNOWN_SCOPES, KNOWN_TOOLS, maxRisk, PERMISSION_MODES, PROVIDERS, SANDBOX_MODES, validateDraft,
  type AgentDraft, type AgentPlan, type ApplyOutcome, type DraftKey, type EditorSection, type ValidationIssue,
} from './plan'
import type { AgentRecord } from './types'

export const inputCls = 'h-9 w-full border border-zinc-800 bg-zinc-950 px-2 font-mono text-xs text-zinc-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300'

export function Field({ label, htmlFor, changed, issues, children }: { label: string; htmlFor?: string; changed?: boolean; issues?: ValidationIssue[]; children: ReactNode }) {
  return (
    <div className={`border-l-2 py-1 pl-3 ${changed ? 'border-amber-300/70' : 'border-transparent'}`}>
      <label htmlFor={htmlFor} className="mb-1 block font-mono text-[11px] uppercase tracking-wider text-zinc-500">{label}{changed && <span className="ml-2 text-amber-300">changed</span>}</label>
      {children}
      {issues?.map((i, n) => <p key={n} className={`mt-1 text-[12px] ${i.level === 'error' ? 'text-rose-300' : 'text-amber-300'}`}>{i.message}</p>)}
    </div>
  )
}

export function ListField({ label, values, suggestions, tone, onChange }: { label: string; values: string[]; suggestions: string[]; tone?: string; onChange: (v: string[]) => void }) {
  const [draft, setDraft] = useState('')
  const all = [...new Set([...suggestions, ...values])]
  const toggle = (x: string) => onChange(values.includes(x) ? values.filter((v) => v !== x) : [...values, x])
  const add = () => {
    const v = draft.trim()
    if (v && !values.includes(v)) onChange([...values, v])
    setDraft('')
  }
  return (
    <div>
      <div className="flex flex-wrap gap-1.5" role="group" aria-label={label}>
        {all.map((x) => {
          const on = values.includes(x)
          return (
            <button key={x} type="button" aria-pressed={on} onClick={() => toggle(x)} className={`border px-1.5 py-0.5 font-mono text-[11px] transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 ${on ? (tone ?? 'border-zinc-500 bg-zinc-800 text-zinc-100') : 'border-zinc-800 text-zinc-500 hover:text-zinc-300'}`}>
              {on ? '' : '+ '}{x}
            </button>
          )
        })}
      </div>
      <input
        aria-label={`Add ${label}`}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && (e.preventDefault(), add())}
        onBlur={add}
        placeholder={`Add ${label.toLowerCase()} and press Enter`}
        className={`${inputCls} mt-2 max-w-xs`}
      />
    </div>
  )
}

export interface AgentEditorProps {
  agent: AgentRecord
  onClose: () => void
  /** Called after a plan applies or rolls back, with the new record. */
  onChanged?: (agent: AgentRecord) => void
}

/**
 * Agent definition editor. Flow: edit -> Review plan (diff, risk, validation)
 * -> Approve (step-up) -> Apply (step-up) -> result with rollback. Approve,
 * apply and rollback are ActionBus actions, so IdentityConfirm always runs.
 */
export function AgentEditor({ agent, onClose, onChanged }: AgentEditorProps) {
  const client = useAgentsClient()
  const edit = useAgentEdit()
  const original = useMemo(() => draftFromAgent(agent), [agent])
  const [draft, setDraft] = useState<AgentDraft>(original)
  const [note, setNote] = useState('')
  const [plan, setPlan] = useState<AgentPlan | null>(null)
  const [outcome, setOutcome] = useState<ApplyOutcome | null>(null)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<string | null>(null)

  const changes = useMemo(() => diffDraft(original, draft), [original, draft])
  const live = !!agent.live && agent.live.state !== 'offline'
  const risks = useMemo(() => assessRisks(original, draft, live), [original, draft, live])
  const risk = maxRisk(risks)
  const issues = useMemo(() => validateDraft(draft, { risk, note }), [draft, risk, note])
  const changed = new Set<DraftKey>(changes.map((c) => c.field))
  const issuesFor = (k: DraftKey | 'note') => issues.filter((i) => i.field === k)
  const set = <K extends DraftKey>(k: K, v: AgentDraft[K]) => setDraft((d) => ({ ...d, [k]: v }))

  if (!edit) return null
  const { bus, identity } = edit

  const fail = (e: unknown) => setMessage(e instanceof Error ? e.message : 'Request failed')

  const createPlan = async () => {
    setBusy(true)
    setMessage(null)
    try {
      setPlan(await client.planChange({ agentId: agent.identity.id, draft, baseRevision: agent.source.revision ?? '', note }))
    } catch (e) {
      fail(e)
    } finally {
      setBusy(false)
    }
  }

  const run = async (kind: AgentActionKind) => {
    if (!plan) return
    setBusy(true)
    setMessage(null)
    try {
      const res = await bus.dispatch(agentPlanAction(kind, plan, agent.identity.name))
      if (res.status === 'denied') setMessage(`Not confirmed: ${res.message ?? 'identity check declined'}. Nothing changed.`)
      else if (res.status === 'unhandled') setMessage('Agent changes are not wired to the action bus.')
      else if (kind === AGENT_ACTIONS.approve) {
        if (res.status === 'ok') setPlan(res.data as AgentPlan)
        else setMessage(res.message ?? 'Approval failed')
      } else {
        const out = res.data as ApplyOutcome | undefined
        if (out) {
          setPlan(out.plan)
          setOutcome(out)
          if (out.agent) onChanged?.(out.agent)
          else if (out.status === 'conflict') client.getAgent(agent.identity.id).then((a) => a && onChanged?.(a))
        } else setMessage(res.message ?? 'Request failed')
      }
    } catch (e) {
      fail(e)
    } finally {
      setBusy(false)
    }
  }

  const sectionBlock = (title: EditorSection, body: ReactNode) => <Block title={title}>{body}</Block>
  const reviewing = plan !== null

  return (
    <article aria-label={`Edit ${agent.identity.name}`}>
      <StepUpHost identity={identity} />
      <header className="flex flex-wrap items-center gap-3 border-b border-zinc-800/80 px-5 py-3">
        <h2 className="font-mono text-sm font-semibold uppercase tracking-wide">Edit {agent.identity.name}</h2>
        <span className="font-mono text-[11px] text-zinc-500">{agent.identity.id} · revision {agent.source.revision ?? 'unknown'}</span>
        <span className="ml-auto flex items-center gap-2">
          {!reviewing && changes.length > 0 && <RiskBadge risk={risk} />}
          <Btn onClick={onClose} disabled={busy}>Close</Btn>
        </span>
      </header>

      {reviewing ? (
        <PlanReview
          plan={plan}
          outcome={outcome}
          busy={busy}
          message={message}
          onBack={() => { setPlan(null); setOutcome(null); setMessage(null) }}
          onApprove={() => run(AGENT_ACTIONS.approve)}
          onApply={() => run(AGENT_ACTIONS.apply)}
          onRollback={() => run(AGENT_ACTIONS.rollback)}
          onDone={onClose}
        />
      ) : (
        <>
          {sectionBlock('Identity', (
            <div className="space-y-3">
              <Field label="Enabled" htmlFor="ed-enabled" changed={changed.has('enabled')}>
                <label className="flex items-center gap-2 text-[13px] text-zinc-200"><input id="ed-enabled" type="checkbox" checked={draft.enabled} onChange={(e) => set('enabled', e.target.checked)} /> Agent can be launched</label>
              </Field>
              <Field label="Summary" htmlFor="ed-summary" changed={changed.has('summary')}>
                <input id="ed-summary" value={draft.summary} onChange={(e) => set('summary', e.target.value)} className={inputCls} />
              </Field>
              <Field label="Roles" changed={changed.has('roles')} issues={issuesFor('roles')}>
                <ListField label="Roles" values={draft.roles} suggestions={[]} onChange={(v) => set('roles', v)} />
              </Field>
            </div>
          ))}
          {sectionBlock('Runtime', (
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Provider" htmlFor="ed-provider" changed={changed.has('provider')} issues={issuesFor('provider')}>
                <select id="ed-provider" value={draft.provider} onChange={(e) => set('provider', e.target.value)} className={inputCls}>
                  {[...new Set([...PROVIDERS, draft.provider].filter(Boolean))].map((p) => <option key={p} value={p}>{p}</option>)}
                </select>
              </Field>
              <Field label="Model" htmlFor="ed-model" changed={changed.has('model')}>
                <input id="ed-model" value={draft.model} onChange={(e) => set('model', e.target.value)} className={inputCls} />
              </Field>
              <Field label="Project" htmlFor="ed-project" changed={changed.has('project')}>
                <input id="ed-project" value={draft.project} onChange={(e) => set('project', e.target.value)} className={inputCls} />
              </Field>
              <Field label="Worker" htmlFor="ed-worker" changed={changed.has('worker')}>
                <input id="ed-worker" value={draft.worker} onChange={(e) => set('worker', e.target.value)} className={inputCls} />
              </Field>
              <Field label="Workspace" htmlFor="ed-workspace" changed={changed.has('workspaceMode')}>
                <select id="ed-workspace" value={draft.workspaceMode} onChange={(e) => set('workspaceMode', e.target.value)} className={inputCls}>
                  {[...new Set(['', 'worktree', 'hybrid', 'direct', draft.workspaceMode])].map((p) => <option key={p} value={p}>{p || 'not set'}</option>)}
                </select>
              </Field>
            </div>
          ))}
          {sectionBlock('Prompts', (
            <div className="space-y-3">
              <Field label="Boot profile" htmlFor="ed-boot" changed={changed.has('bootProfile')}>
                <input id="ed-boot" value={draft.bootProfile} onChange={(e) => set('bootProfile', e.target.value)} className={`${inputCls} max-w-xs`} />
              </Field>
              <Field label={`Prompt (${draft.prompt.length}/8000)`} htmlFor="ed-prompt" changed={changed.has('prompt')} issues={issuesFor('prompt')}>
                <textarea id="ed-prompt" value={draft.prompt} onChange={(e) => set('prompt', e.target.value)} rows={4} className={`${inputCls} h-auto py-1.5`} />
              </Field>
            </div>
          ))}
          {sectionBlock('Tools and skills', (
            <div className="space-y-3">
              <Field label="Tool allowlist" changed={changed.has('tools')} issues={issuesFor('tools')}>
                <ListField label="Tools" values={draft.tools} suggestions={KNOWN_TOOLS} tone="border-emerald-400/50 bg-emerald-400/10 text-emerald-300" onChange={(v) => set('tools', v)} />
              </Field>
              <Field label="Skills" changed={changed.has('skills')} issues={issuesFor('skills')}>
                <ListField label="Skills" values={draft.skills} suggestions={[]} onChange={(v) => set('skills', v)} />
              </Field>
            </div>
          ))}
          {sectionBlock('Permissions and policy', (
            <div className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="Permission mode" htmlFor="ed-perm" changed={changed.has('permissionMode')} issues={issuesFor('permissionMode')}>
                  <select id="ed-perm" value={draft.permissionMode} onChange={(e) => set('permissionMode', e.target.value)} className={inputCls}>
                    {[...new Set([...PERMISSION_MODES, draft.permissionMode].filter(Boolean))].map((p) => <option key={p} value={p}>{p}</option>)}
                  </select>
                </Field>
                <Field label="Sandbox" htmlFor="ed-sandbox" changed={changed.has('sandbox')} issues={issuesFor('sandbox')}>
                  <select id="ed-sandbox" value={draft.sandbox} onChange={(e) => set('sandbox', e.target.value)} className={inputCls}>
                    {[...new Set(['', ...SANDBOX_MODES, draft.sandbox])].map((p) => <option key={p} value={p}>{p || 'not set'}</option>)}
                  </select>
                </Field>
              </div>
              <Field label="Network" htmlFor="ed-network" changed={changed.has('network')}>
                <label className="flex items-center gap-2 text-[13px] text-zinc-200"><input id="ed-network" type="checkbox" checked={draft.network} onChange={(e) => set('network', e.target.checked)} /> Allow network access</label>
              </Field>
              <Field label="Privileged scopes" changed={changed.has('scopes')} issues={issuesFor('scopes')}>
                <ListField label="Scopes" values={draft.scopes} suggestions={KNOWN_SCOPES} tone="border-amber-400/60 bg-amber-400/10 text-amber-300" onChange={(v) => set('scopes', v)} />
              </Field>
              <Field label="Policy rules" changed={changed.has('rules')} issues={issuesFor('rules')}>
                <ListField label="Rules" values={draft.rules} suggestions={KNOWN_RULES} onChange={(v) => set('rules', v)} />
              </Field>
              <p className="text-[12px] text-zinc-500">Rule ids follow the policy-plane vocabulary; unknown ids are accepted but flagged.</p>
            </div>
          ))}
          <Block title="Reason">
            <Field label={risk === 'high' ? 'Reason (required for high risk)' : 'Reason (optional)'} htmlFor="ed-note" issues={issuesFor('note')}>
              <input id="ed-note" value={note} onChange={(e) => setNote(e.target.value)} className={inputCls} placeholder="Why this change" />
            </Field>
          </Block>
          {message && <div role="alert" className="border-b border-rose-400/30 bg-rose-400/5 px-5 py-2 text-[13px] text-rose-200">{message}</div>}
          <div className="sticky bottom-0 flex flex-wrap items-center gap-3 border-t border-zinc-800 bg-zinc-950 px-5 py-3">
            <span className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">{changes.length} change{changes.length === 1 ? '' : 's'}</span>
            {risks.filter((r) => r.level === 'high').length > 0 && <Chip tone={'border-rose-400/60 text-rose-300'}>{risks.filter((r) => r.level === 'high').length} high-risk</Chip>}
            {issues.some((i) => i.level === 'error') && <span className="text-[12px] text-rose-300">{issues.filter((i) => i.level === 'error').length} problem(s) to fix</span>}
            <span className="ml-auto flex gap-2">
              <Btn onClick={() => { setDraft(original); setNote('') }} disabled={changes.length === 0 || busy}>Reset</Btn>
              <Btn tone="primary" onClick={createPlan} disabled={changes.length === 0 || busy || issues.some((i) => i.level === 'error')}>Review plan</Btn>
            </span>
          </div>
        </>
      )}
    </article>
  )
}
