// Change planning for the agent editor (B2). Pure functions: no I/O, no React.
// A PLAN is the typed result of comparing an edited draft with the current
// definition: a field diff, risk flags for privileged changes and validation
// issues. The client (mock now, backend later) owns plan identity and state;
// these helpers give the GUI an instant local preview and give the mock a
// reference implementation of what the server must compute authoritatively.

import type { ConfirmResult, ConfirmWho } from '../seams/identity-confirm'
import type { LifecycleDetail, TeamRecord } from './lifecycle'
import type { AgentRecord, Risk } from './types'

/** The editable surface of an agent definition, flattened. */
export interface AgentDraft {
  enabled: boolean
  summary: string
  roles: string[]
  provider: string
  model: string
  project: string
  worker: string
  workspaceMode: string
  bootProfile: string
  prompt: string
  tools: string[]
  skills: string[]
  permissionMode: string
  sandbox: string
  network: boolean
  scopes: string[]
  rules: string[]
}

export type DraftKey = keyof AgentDraft
export type FieldKind = 'bool' | 'text' | 'list'
export type EditorSection = 'Identity' | 'Runtime' | 'Prompts' | 'Tools and skills' | 'Permissions and policy'

export interface FieldDef {
  key: DraftKey
  label: string
  section: EditorSection
  kind: FieldKind
}

export const FIELDS: FieldDef[] = [
  { key: 'enabled', label: 'Enabled', section: 'Identity', kind: 'bool' },
  { key: 'summary', label: 'Summary', section: 'Identity', kind: 'text' },
  { key: 'roles', label: 'Roles', section: 'Identity', kind: 'list' },
  { key: 'provider', label: 'Provider', section: 'Runtime', kind: 'text' },
  { key: 'model', label: 'Model', section: 'Runtime', kind: 'text' },
  { key: 'project', label: 'Project', section: 'Runtime', kind: 'text' },
  { key: 'worker', label: 'Worker', section: 'Runtime', kind: 'text' },
  { key: 'workspaceMode', label: 'Workspace', section: 'Runtime', kind: 'text' },
  { key: 'bootProfile', label: 'Boot profile', section: 'Prompts', kind: 'text' },
  { key: 'prompt', label: 'Prompt', section: 'Prompts', kind: 'text' },
  { key: 'tools', label: 'Tool allowlist', section: 'Tools and skills', kind: 'list' },
  { key: 'skills', label: 'Skills', section: 'Tools and skills', kind: 'list' },
  { key: 'permissionMode', label: 'Permission mode', section: 'Permissions and policy', kind: 'text' },
  { key: 'sandbox', label: 'Sandbox', section: 'Permissions and policy', kind: 'text' },
  { key: 'network', label: 'Network', section: 'Permissions and policy', kind: 'bool' },
  { key: 'scopes', label: 'Privileged scopes', section: 'Permissions and policy', kind: 'list' },
  { key: 'rules', label: 'Policy rules', section: 'Permissions and policy', kind: 'list' },
]

export const PERMISSION_MODES = ['default', 'bypass']
export const SANDBOX_MODES = ['workspace', 'unrestricted']
export const PROVIDERS = ['claude-code', 'codex-app-server', 'opencode']
export const KNOWN_TOOLS = ['torque', 'tesseract', 'tangent', 'playwright', 'shadcn', 'cerberus']
export const KNOWN_SCOPES = ['message.write', 'session.write', 'catalog.write']
export const KNOWN_RULES = ['no-secrets', 'no-main-push', 'no-self-approve', 'read-only', 'ask-outward', 'branch-pr', 'relay-only', 'not-implementer', 'report-to-coordinator']

/** Scopes that let an agent change the platform itself. */
export const HIGH_SCOPES = ['session.write', 'catalog.write']
/** Guard rules whose removal widens what the agent may do. */
export const GUARD_RULES = ['no-secrets', 'no-main-push', 'no-self-approve', 'read-only']
const SENSITIVE_TOOLS = ['cerberus']
const PROMPT_MAX = 8000

export type FieldValue = string | boolean | string[]

export function draftFromAgent(a: AgentRecord): AgentDraft {
  return {
    enabled: a.identity.enabled,
    summary: a.identity.summary ?? '',
    roles: [...a.identity.roles],
    provider: a.runtime.provider,
    model: a.runtime.model ?? '',
    project: a.runtime.project ?? '',
    worker: a.runtime.worker ?? '',
    workspaceMode: a.runtime.workspaceMode ?? '',
    bootProfile: a.runtime.bootProfile ?? '',
    prompt: a.runtime.prompt ?? '',
    tools: [...a.tools],
    skills: [...a.skills],
    permissionMode: a.policy.permissionMode,
    sandbox: a.policy.sandbox ?? '',
    network: a.policy.network ?? false,
    scopes: [...a.policy.scopes],
    rules: [...a.policy.rules],
  }
}

/** Returns a new record with the draft applied. Empty strings clear optional fields. */
export function applyDraft(a: AgentRecord, d: AgentDraft): AgentRecord {
  const opt = (v: string) => (v.trim() === '' ? undefined : v.trim())
  return {
    ...a,
    identity: { ...a.identity, enabled: d.enabled, summary: opt(d.summary), roles: [...d.roles] },
    runtime: {
      ...a.runtime,
      provider: d.provider.trim(),
      model: opt(d.model),
      project: opt(d.project),
      worker: opt(d.worker),
      workspaceMode: opt(d.workspaceMode),
      bootProfile: opt(d.bootProfile),
      prompt: d.prompt.trim() === '' ? undefined : d.prompt,
    },
    policy: { ...a.policy, permissionMode: d.permissionMode, sandbox: opt(d.sandbox), network: d.network, scopes: [...d.scopes], rules: [...d.rules] },
    tools: [...d.tools],
    skills: [...d.skills],
  }
}

export interface FieldChange {
  field: DraftKey
  label: string
  section: EditorSection
  kind: FieldKind
  before: FieldValue
  after: FieldValue
  /** List fields only. */
  added?: string[]
  removed?: string[]
}

export function diffDraft(before: AgentDraft, after: AgentDraft): FieldChange[] {
  const out: FieldChange[] = []
  for (const f of FIELDS) {
    const b = before[f.key]
    const a = after[f.key]
    if (f.kind === 'list') {
      const bl = b as string[]
      const al = a as string[]
      const added = al.filter((x) => !bl.includes(x))
      const removed = bl.filter((x) => !al.includes(x))
      if (added.length || removed.length) out.push({ field: f.key, label: f.label, section: f.section, kind: f.kind, before: bl, after: al, added, removed })
    } else if (b !== a) {
      out.push({ field: f.key, label: f.label, section: f.section, kind: f.kind, before: b, after: a })
    }
  }
  return out
}

export interface RiskFlag {
  id: string
  level: Risk
  field: DraftKey | 'lifecycle'
  title: string
  detail: string
}

const RANK: Record<Risk, number> = { low: 0, medium: 1, high: 2 }
export const maxRisk = (flags: RiskFlag[]): Risk => flags.reduce<Risk>((m, f) => (RANK[f.level] > RANK[m] ? f.level : m), 'low')

export function assessRisks(before: AgentDraft, after: AgentDraft, live = false): RiskFlag[] {
  const r: RiskFlag[] = []
  if (after.permissionMode === 'bypass' && before.permissionMode !== 'bypass')
    r.push({ id: 'perm-bypass', level: 'high', field: 'permissionMode', title: 'Bypasses permission prompts', detail: 'The agent will run tools without asking.' })
  if (after.sandbox === 'unrestricted' && before.sandbox !== 'unrestricted')
    r.push({ id: 'sandbox-unrestricted', level: 'high', field: 'sandbox', title: 'Unrestricted sandbox', detail: 'File and process access is no longer limited to the workspace.' })
  for (const s of after.scopes.filter((x) => !before.scopes.includes(x)))
    r.push({ id: `scope-${s}`, level: HIGH_SCOPES.includes(s) ? 'high' : 'medium', field: 'scopes', title: `Grants privileged scope ${s}`, detail: HIGH_SCOPES.includes(s) ? 'Lets the agent change platform state.' : 'Lets the agent act outward on your behalf.' })
  for (const g of before.rules.filter((x) => !after.rules.includes(x)))
    r.push({ id: `rule-${g}`, level: GUARD_RULES.includes(g) ? 'high' : 'medium', field: 'rules', title: `Removes policy rule ${g}`, detail: GUARD_RULES.includes(g) ? 'A guard rule is being lifted.' : 'A behavioural constraint is being lifted.' })
  if (after.network && !before.network)
    r.push({ id: 'network-on', level: 'medium', field: 'network', title: 'Enables network access', detail: 'The agent can reach external hosts.' })
  for (const t of after.tools.filter((x) => !before.tools.includes(x)))
    r.push({ id: `tool-${t}`, level: SENSITIVE_TOOLS.includes(t) ? 'high' : 'low', field: 'tools', title: `Adds tool ${t}`, detail: SENSITIVE_TOOLS.includes(t) ? 'Operates infrastructure.' : 'Extends what the agent can call.' })
  if (live && (before.provider !== after.provider || before.model !== after.model))
    r.push({ id: 'runtime-live', level: 'medium', field: 'provider', title: 'Provider or model change on a running agent', detail: 'Live sessions keep the old runtime until restarted.' })
  if (live && before.enabled && !after.enabled)
    r.push({ id: 'disable-live', level: 'medium', field: 'enabled', title: 'Disables a running agent', detail: 'New launches are blocked; running sessions continue.' })
  if (after.prompt !== before.prompt || after.bootProfile !== before.bootProfile)
    r.push({ id: 'prompt-change', level: 'low', field: 'prompt', title: 'Changes boot instructions', detail: 'Affects how every new session starts.' })
  return r
}

export type IssueLevel = 'error' | 'warning'
export interface ValidationIssue {
  level: IssueLevel
  field: DraftKey | 'note' | 'lifecycle'
  message: string
}

const SCOPE_RE = /^[a-z][a-z0-9]*(\.[a-z][a-z0-9_]*)+$/
const RULE_RE = /^[a-z][a-z0-9]*(-[a-z0-9]+)*$/

export function validateDraft(d: AgentDraft, opts: { risk?: Risk; note?: string } = {}): ValidationIssue[] {
  const v: ValidationIssue[] = []
  const err = (field: ValidationIssue['field'], message: string) => v.push({ level: 'error', field, message })
  const warn = (field: ValidationIssue['field'], message: string) => v.push({ level: 'warning', field, message })
  if (!d.provider.trim()) err('provider', 'Provider is required.')
  if (!d.permissionMode.trim()) err('permissionMode', 'Permission mode is required.')
  else if (!PERMISSION_MODES.includes(d.permissionMode)) warn('permissionMode', `Unrecognised permission mode "${d.permissionMode}".`)
  if (d.sandbox && !SANDBOX_MODES.includes(d.sandbox)) warn('sandbox', `Unrecognised sandbox "${d.sandbox}".`)
  if (d.prompt.length > PROMPT_MAX) err('prompt', `Prompt is ${d.prompt.length} characters; the limit is ${PROMPT_MAX}.`)
  if (d.roles.length === 0) warn('roles', 'No roles: the agent cannot be addressed by role.')
  for (const k of ['roles', 'tools', 'skills', 'scopes', 'rules'] as const) {
    const dup = d[k].find((x, i) => d[k].indexOf(x) !== i)
    if (dup) err(k, `Duplicate entry "${dup}".`)
  }
  for (const s of d.scopes) if (!SCOPE_RE.test(s)) err('scopes', `Scope "${s}" must look like area.action.`)
  for (const s of d.scopes) if (SCOPE_RE.test(s) && !KNOWN_SCOPES.includes(s)) warn('scopes', `Scope "${s}" is not in the known scope list.`)
  for (const s of d.rules) if (!RULE_RE.test(s)) err('rules', `Rule "${s}" must be a lowercase-hyphen id.`)
  if (d.rules.includes('read-only') && d.scopes.some((s) => HIGH_SCOPES.includes(s))) err('rules', 'Rule read-only contradicts write scopes.')
  if (d.permissionMode === 'bypass' && d.sandbox === 'unrestricted') warn('sandbox', 'Bypass permissions with an unrestricted sandbox removes both safety layers.')
  if (d.tools.length === 0) warn('tools', 'Empty tool allowlist: the agent falls back to the project default.')
  if (opts.risk === 'high' && !(opts.note ?? '').trim()) err('note', 'A reason is required for high-risk changes.')
  return v
}

export const hasErrors = (v: ValidationIssue[]) => v.some((i) => i.level === 'error')

// ---- plan, approval, apply -------------------------------------------------

export type PlanStatus = 'proposed' | 'invalid' | 'approved' | 'applied' | 'failed' | 'conflict' | 'rolled_back' | 'expired'

export interface PlanApproval {
  by: ConfirmWho
  at: number
  /** The approval lapses; apply after this needs a fresh approval. */
  expiresAt: number
  scope: string[]
}

export interface AgentPlan {
  id: string
  agentId: string
  /** Definition revision the plan was computed against; apply fails with `conflict` if it moved. */
  baseRevision: string
  createdAt: number
  createdBy: ConfirmWho
  note?: string
  status: PlanStatus
  draft: AgentDraft
  changes: FieldChange[]
  risks: RiskFlag[]
  risk: Risk
  issues: ValidationIssue[]
  approval?: PlanApproval
  /** Scopes the step-up must cover to approve or apply. */
  requiredScope: string[]
  /** Present for create, retire, team and assignment plans (B3). `agentId` then names the plan's subject (agent id, team id or "assignments"). */
  lifecycle?: LifecycleDetail
}

export interface PlanRequest {
  agentId: string
  draft: AgentDraft
  baseRevision?: string
  note?: string
  actor?: ConfirmWho
}

export type ApplyStatus = 'applied' | 'failed' | 'conflict' | 'expired' | 'rolled_back'

export interface ApplyOutcome {
  status: ApplyStatus
  plan: AgentPlan
  agent?: AgentRecord
  /** Set when a team plan applied or rolled back. */
  team?: TeamRecord
  revisionBefore?: string
  revisionAfter?: string
  auditId?: string
  message?: string
}

export type AuditKind = 'plan' | 'approve' | 'apply' | 'apply_failed' | 'rollback'

export interface AuditEntry {
  id: string
  agentId: string
  planId: string
  kind: AuditKind
  at: number
  actor: ConfirmWho
  summary: string
  risk: Risk
  changeCount: number
  revisionBefore?: string
  revisionAfter?: string
  /** What the step-up covered; the token itself is never stored client-side. */
  confirmation?: Pick<ConfirmResult, 'outcome' | 'scope' | 'issuedAt' | 'expiresAt'>
}

/** Scopes a step-up must cover: write on the agent plus any privileged scope it touches. */
export function requiredScopeFor(agentId: string, changes: FieldChange[], risks: RiskFlag[]): string[] {
  const scope = ['agent.write', `agent:${agentId}`]
  if (risks.some((r) => r.level === 'high')) scope.push('agent.write.privileged')
  if (changes.some((c) => c.field === 'scopes' && c.added?.length)) scope.push('policy.grant')
  return scope
}

export function computePlan(agent: AgentRecord, req: PlanRequest, meta: { id: string; now: number; actor: ConfirmWho }): AgentPlan {
  const before = draftFromAgent(agent)
  const changes = diffDraft(before, req.draft)
  const risks = assessRisks(before, req.draft, !!agent.live && agent.live.state !== 'offline')
  const risk = maxRisk(risks)
  const issues = validateDraft(req.draft, { risk, note: req.note })
  if (changes.length === 0) issues.push({ level: 'error', field: 'note', message: 'No changes to plan.' })
  return {
    id: meta.id,
    agentId: agent.identity.id,
    baseRevision: req.baseRevision ?? agent.source.revision ?? '',
    createdAt: meta.now,
    createdBy: meta.actor,
    note: req.note?.trim() || undefined,
    status: hasErrors(issues) ? 'invalid' : 'proposed',
    draft: req.draft,
    changes,
    risks,
    risk,
    issues,
    requiredScope: requiredScopeFor(agent.identity.id, changes, risks),
  }
}
