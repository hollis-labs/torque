// Agent lifecycle planning (B3): create, clone, retire/restore, teams and
// assignments. Pure functions, no I/O, no React. Every lifecycle change is an
// AgentPlan (the B2 type) with a `lifecycle` block, so it flows through the
// same plan -> approve -> apply path and IdentityConfirm step-up as an edit.
// The mock client uses these helpers as the reference for what the server must
// compute authoritatively (see BACKEND_NEEDS.md).

import type { ConfirmWho } from '../seams/identity-confirm'
import {
  assessRisks, diffDraft, GUARD_RULES, hasErrors, HIGH_SCOPES, maxRisk, validateDraft,
  type AgentDraft, type AgentPlan, type FieldChange, type RiskFlag, type ValidationIssue,
} from './plan'
import { isPrivileged } from './filter'
import type { AgentRecord, Risk } from './types'

export type LifecycleOp =
  | 'agent.create'
  | 'agent.retire'
  | 'agent.restore'
  | 'team.create'
  | 'team.update'
  | 'team.delete'
  | 'assignment.update'

export const OP_LABEL: Record<LifecycleOp, string> = {
  'agent.create': 'create agent',
  'agent.retire': 'retire agent',
  'agent.restore': 'restore agent',
  'team.create': 'create team',
  'team.update': 'update team',
  'team.delete': 'delete team',
  'assignment.update': 'change assignments',
}

// ---- teams ------------------------------------------------------------------

export const TEAM_ROLES = ['coordinator', 'worker', 'reviewer', 'proxy', 'observer']
export const TEAM_ID_RE = /^tm_[a-z0-9][a-z0-9_-]{1,38}$/

export interface TeamMember {
  agentId: string
  role: string
}

/** Shared policy: a ceiling every member is expected to stay within, plus team-wide rules. */
export interface TeamPolicy {
  permissionCeiling: string
  scopesCeiling: string[]
  rules: string[]
  /** Agent id (or URN) that results are reported to. */
  reportTo?: string
}

export interface TeamDraft {
  name: string
  summary: string
  members: TeamMember[]
  policy: TeamPolicy
}

export interface TeamRecord extends TeamDraft {
  id: string
  revision: string
  updatedAt: number | null
}

export const blankTeamDraft = (): TeamDraft => ({
  name: '',
  summary: '',
  members: [],
  policy: { permissionCeiling: 'default', scopesCeiling: [], rules: ['no-secrets'] },
})

export const teamDraftFrom = (t: TeamRecord): TeamDraft => structuredClone({ name: t.name, summary: t.summary, members: t.members, policy: t.policy })

// ---- assignments ------------------------------------------------------------

export type AssignmentTargetType = 'workstream' | 'task'

export interface AssignmentTarget {
  type: AssignmentTargetType
  id: string
  label?: string
}

export interface AssignmentRecord {
  id: string
  agentId: string
  target: AssignmentTarget
  role?: string
  since: number
}

export interface AssignmentDraft {
  agentId: string
  target: AssignmentTarget
  role?: string
}

// ---- templates --------------------------------------------------------------

export interface AgentTemplate {
  id: string
  name: string
  summary: string
  /** Fields the template sets; everything else comes from blankDraft(). */
  draft: Partial<AgentDraft>
}

export const AGENT_ID_RE = /^[a-z][a-z0-9-]{1,39}$/

/** Safe starting point: asks before risky tools, workspace sandbox, no network, no privileged scopes. */
export function blankDraft(): AgentDraft {
  return {
    enabled: true, summary: '', roles: [], provider: 'claude-code', model: '', project: '', worker: '', workspaceMode: 'worktree',
    bootProfile: '', prompt: '', tools: [], skills: [], permissionMode: 'default', sandbox: 'workspace', network: false, scopes: [], rules: ['no-secrets'],
  }
}

export const draftFromTemplate = (t: AgentTemplate): AgentDraft => ({ ...blankDraft(), ...structuredClone(t.draft) })

/** Clone keeps the definition, including privileges (the plan flags them), and starts enabled. */
export function draftFromClone(a: AgentRecord): AgentDraft {
  return {
    enabled: true,
    summary: a.identity.summary ?? '', roles: [...a.identity.roles], provider: a.runtime.provider, model: a.runtime.model ?? '',
    project: a.runtime.project ?? '', worker: a.runtime.worker ?? '', workspaceMode: a.runtime.workspaceMode ?? '',
    bootProfile: a.runtime.bootProfile ?? '', prompt: a.runtime.prompt ?? '', tools: [...a.tools], skills: [...a.skills],
    permissionMode: a.policy.permissionMode, sandbox: a.policy.sandbox ?? '', network: a.policy.network ?? false, scopes: [...a.policy.scopes], rules: [...a.policy.rules],
  }
}

export function slugify(name: string): string {
  return name.toLowerCase().trim().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40)
}

/** Builds an AgentRecord from a create plan's draft. */
export function recordFromDraft(id: string, name: string, d: AgentDraft, meta: { now: number; revision: string; source?: string }): AgentRecord {
  const opt = (v: string) => (v.trim() === '' ? undefined : v.trim())
  return {
    identity: { id, name: name.trim(), roles: [...d.roles], summary: opt(d.summary), enabled: d.enabled },
    runtime: { provider: d.provider.trim(), model: opt(d.model), project: opt(d.project), worker: opt(d.worker), workspaceMode: opt(d.workspaceMode), bootProfile: opt(d.bootProfile), prompt: d.prompt.trim() === '' ? undefined : d.prompt },
    policy: { permissionMode: d.permissionMode, sandbox: opt(d.sandbox), network: d.network, scopes: [...d.scopes], rules: [...d.rules] },
    tools: [...d.tools],
    skills: [...d.skills],
    teams: [],
    source: { path: meta.source ?? `catalog/agents/${id}.yaml`, revision: meta.revision, updatedAt: meta.now },
  }
}

// ---- requests and plan detail ----------------------------------------------

export type RetireSessions = 'drain' | 'stop'

export type LifecycleRequest =
  | { op: 'agent.create'; agentId: string; name: string; draft: AgentDraft; cloneOf?: string; templateId?: string }
  | { op: 'agent.retire'; agentId: string; sessions: RetireSessions }
  | { op: 'agent.restore'; agentId: string }
  | { op: 'team.create'; teamId: string; team: TeamDraft }
  | { op: 'team.update'; teamId: string; team: TeamDraft }
  | { op: 'team.delete'; teamId: string }
  | { op: 'assignment.update'; add: AssignmentDraft[]; remove: string[] }

export type LifecyclePlanRequest = LifecycleRequest & { note?: string; actor?: ConfirmWho; baseRevision?: string }

export interface LifecycleItem {
  tone: 'add' | 'remove' | 'change' | 'note'
  label: string
  detail?: string
}

export interface RetireImpact {
  agentId: string
  /** Live sessions that the retire affects. */
  sessions: { count: number; state: string }
  tasks: AssignmentRecord[]
  workstreams: AssignmentRecord[]
  teams: { teamId: string; teamName: string; role: string; soleCoordinator: boolean }[]
}

export interface LifecycleDetail {
  request: LifecycleRequest
  summary: string
  items: LifecycleItem[]
  impact?: RetireImpact
}

export interface PlanContext {
  agents: AgentRecord[]
  teams: TeamRecord[]
  assignments: AssignmentRecord[]
}

export function computeRetireImpact(agent: AgentRecord, ctx: PlanContext): RetireImpact {
  const id = agent.identity.id
  const mine = ctx.assignments.filter((a) => a.agentId === id)
  return {
    agentId: id,
    sessions: { count: agent.live?.sessions ?? 0, state: agent.live?.state ?? 'offline' },
    tasks: mine.filter((a) => a.target.type === 'task'),
    workstreams: mine.filter((a) => a.target.type === 'workstream'),
    teams: ctx.teams
      .filter((t) => t.members.some((m) => m.agentId === id))
      .map((t) => {
        const role = t.members.find((m) => m.agentId === id)!.role
        const soleCoordinator = role === 'coordinator' && !t.members.some((m) => m.agentId !== id && m.role === 'coordinator')
        return { teamId: t.id, teamName: t.name, role, soleCoordinator }
      }),
  }
}

export const impactTotal = (i: RetireImpact) => i.sessions.count + i.tasks.length + i.workstreams.length + i.teams.length

// ---- plan assembly ----------------------------------------------------------

interface Parts {
  req: LifecyclePlanRequest
  subjectId: string
  area: 'agent.create' | 'agent.retire' | 'agent.restore' | 'team.write' | 'assignment.write'
  summary: string
  draft: AgentDraft
  changes?: FieldChange[]
  risks: RiskFlag[]
  issues: ValidationIssue[]
  items: LifecycleItem[]
  impact?: RetireImpact
  baseRevision: string
  /** Extra subject scopes (assignments touch several agents). */
  scopes?: string[]
}

const flag = (id: string, level: Risk, title: string, detail: string): RiskFlag => ({ id, level, field: 'lifecycle', title, detail })
const issue = (level: 'error' | 'warning', message: string): ValidationIssue => ({ level, field: 'lifecycle', message })

function assemble(p: Parts, meta: { id: string; now: number; actor: ConfirmWho }): AgentPlan {
  const risk = maxRisk(p.risks)
  const issues = [...p.issues]
  if (risk === 'high' && !(p.req.note ?? '').trim() && !issues.some((i) => i.field === 'note')) issues.push({ level: 'error', field: 'note', message: 'A reason is required for high-risk changes.' })
  const requiredScope = [p.area, ...(p.scopes ?? [`${p.area.startsWith('team') ? 'team' : 'agent'}:${p.subjectId}`])]
  if (risk === 'high') requiredScope.push('agent.write.privileged')
  if (p.risks.some((r) => r.id.startsWith('scope-'))) requiredScope.push('policy.grant')
  const { op } = p.req
  const { op: _op, note: _n, actor: _a, baseRevision: _b, ...rest } = p.req
  void _op; void _n; void _a; void _b
  return {
    id: meta.id,
    agentId: p.subjectId,
    baseRevision: p.baseRevision,
    createdAt: meta.now,
    createdBy: meta.actor,
    note: p.req.note?.trim() || undefined,
    status: hasErrors(issues) ? 'invalid' : 'proposed',
    draft: p.draft,
    changes: p.changes ?? [],
    risks: p.risks,
    risk,
    issues,
    requiredScope: [...new Set(requiredScope)],
    lifecycle: { request: { op, ...rest } as LifecycleRequest, summary: p.summary, items: p.items, impact: p.impact },
  }
}

export function computeLifecyclePlan(req: LifecyclePlanRequest, ctx: PlanContext, meta: { id: string; now: number; actor: ConfirmWho }): AgentPlan {
  switch (req.op) {
    case 'agent.create': return planCreate(req, ctx, meta)
    case 'agent.retire':
    case 'agent.restore': return planRetire(req, ctx, meta)
    case 'team.create':
    case 'team.update':
    case 'team.delete': return planTeam(req, ctx, meta)
    case 'assignment.update': return planAssignments(req, ctx, meta)
  }
}

type M = { id: string; now: number; actor: ConfirmWho }

function planCreate(req: Extract<LifecyclePlanRequest, { op: 'agent.create' }>, ctx: PlanContext, meta: M): AgentPlan {
  const blank = blankDraft()
  const risks = assessRisks(blank, req.draft)
  const risk = maxRisk(risks)
  const issues = validateDraft(req.draft, { risk, note: req.note })
  if (!AGENT_ID_RE.test(req.agentId)) issues.push(issue('error', 'Id must be 2-40 characters: lowercase letters, digits and hyphens, starting with a letter.'))
  else if (ctx.agents.some((a) => a.identity.id === req.agentId)) issues.push(issue('error', `An agent with id "${req.agentId}" already exists.`))
  if (!req.name.trim()) issues.push(issue('error', 'Name is required.'))
  const origin = req.cloneOf ? `clone of ${req.cloneOf}` : req.templateId ? `template ${req.templateId}` : 'from scratch'
  const items: LifecycleItem[] = [
    { tone: 'add', label: `Create agent ${req.name.trim() || req.agentId}`, detail: `${req.agentId} · ${origin}` },
    { tone: 'note', label: req.draft.enabled ? 'Starts enabled' : 'Starts disabled', detail: req.draft.enabled ? 'It can be launched as soon as the plan applies.' : 'Enable it from the editor when ready.' },
  ]
  return assemble({ req, subjectId: req.agentId, area: 'agent.create', summary: `Create ${req.agentId}${risk === 'high' ? ' (privileged)' : ''}`, draft: req.draft, changes: diffDraft(blank, req.draft), risks, issues, items, baseRevision: '' }, meta)
}

function planRetire(req: Extract<LifecyclePlanRequest, { op: 'agent.retire' | 'agent.restore' }>, ctx: PlanContext, meta: M): AgentPlan {
  const agent = ctx.agents.find((a) => a.identity.id === req.agentId)
  if (!agent) throw new Error(`Unknown agent ${req.agentId}`)
  const retire = req.op === 'agent.retire'
  const issues: ValidationIssue[] = []
  const items: LifecycleItem[] = []
  const risks: RiskFlag[] = []
  let impact: RetireImpact | undefined
  if (retire) {
    if (agent.identity.retired) issues.push(issue('error', 'Agent is already retired.'))
    impact = computeRetireImpact(agent, ctx)
    risks.push(flag('retire', 'high', 'Retires the agent', 'It is disabled, leaves its teams and releases its assignments. Restoring does not bring those back.'))
    items.push({ tone: 'remove', label: `Retire ${agent.identity.name}`, detail: 'Disabled and hidden from launch pickers; history is kept.' })
    if (impact.sessions.count > 0) {
      const stop = req.sessions === 'stop'
      items.push({ tone: stop ? 'remove' : 'change', label: stop ? `Stop ${impact.sessions.count} running session${impact.sessions.count === 1 ? '' : 's'} now` : `Let ${impact.sessions.count} running session${impact.sessions.count === 1 ? '' : 's'} drain`, detail: stop ? 'In-flight work is interrupted.' : 'No new launches; running sessions finish on their own.' })
      risks.push(flag('sessions', stop ? 'high' : 'medium', stop ? 'Interrupts running sessions' : 'Has running sessions', stop ? 'Unfinished work in those sessions is lost.' : 'They keep running with the old definition until they end.'))
    }
    for (const a of [...impact.tasks, ...impact.workstreams]) items.push({ tone: 'remove', label: `Release ${a.target.type} ${a.target.id}`, detail: a.target.label })
    if (impact.tasks.length) risks.push(flag('tasks', 'medium', `Releases ${impact.tasks.length} assigned task${impact.tasks.length === 1 ? '' : 's'}`, 'They return to the queue unassigned.'))
    for (const t of impact.teams) {
      items.push({ tone: 'remove', label: `Leave team ${t.teamName}`, detail: t.role })
      if (t.soleCoordinator) risks.push(flag(`coord-${t.teamId}`, 'high', `Leaves ${t.teamName} without a coordinator`, 'No remaining member holds the coordinator role.'))
    }
    if (isPrivileged(agent)) items.push({ tone: 'note', label: 'Privileged scopes are revoked with the agent', detail: agent.policy.scopes.join(', ') || undefined })
  } else {
    if (!agent.identity.retired) issues.push(issue('error', 'Agent is not retired.'))
    risks.push(flag('restore', isPrivileged(agent) ? 'high' : 'medium', isPrivileged(agent) ? 'Restores a privileged agent' : 'Restores a retired agent', isPrivileged(agent) ? 'Its bypass, unrestricted or scoped privileges come back with it.' : 'It returns to the directory, still disabled.'))
    items.push({ tone: 'add', label: `Restore ${agent.identity.name}`, detail: 'Comes back disabled with no teams or assignments. Enable it from the editor.' })
  }
  return assemble({ req, subjectId: agent.identity.id, area: retire ? 'agent.retire' : 'agent.restore', summary: `${retire ? 'Retire' : 'Restore'} ${agent.identity.id}`, draft: blankDraft(), risks, issues, items, impact, baseRevision: req.baseRevision ?? agent.source.revision ?? '' }, meta)
}

function planTeam(req: Extract<LifecyclePlanRequest, { op: 'team.create' | 'team.update' | 'team.delete' }>, ctx: PlanContext, meta: M): AgentPlan {
  const existing = ctx.teams.find((t) => t.id === req.teamId)
  const issues: ValidationIssue[] = []
  const items: LifecycleItem[] = []
  const risks: RiskFlag[] = []
  const draft = req.op === 'team.delete' ? undefined : req.team
  const label = existing?.name ?? (draft?.name || req.teamId)

  if (req.op === 'team.create') {
    if (!TEAM_ID_RE.test(req.teamId)) issues.push(issue('error', 'Team id must start with tm_ and use lowercase letters, digits, hyphens or underscores.'))
    if (existing) issues.push(issue('error', `Team "${req.teamId}" already exists.`))
    items.push({ tone: 'add', label: `Create team ${draft!.name.trim() || req.teamId}` })
  } else if (!existing) {
    issues.push(issue('error', `Unknown team ${req.teamId}.`))
  }

  if (req.op === 'team.delete' && existing) {
    risks.push(flag('team-delete', 'high', 'Disbands the team', 'Every member loses this membership and the shared policy stops applying.'))
    items.push({ tone: 'remove', label: `Delete team ${existing.name}` })
    for (const m of existing.members) items.push({ tone: 'remove', label: `${m.agentId} leaves`, detail: m.role })
  }

  if (draft) {
    if (!draft.name.trim()) issues.push(issue('error', 'Team name is required.'))
    else if (ctx.teams.some((t) => t.id !== req.teamId && t.name.toLowerCase() === draft.name.trim().toLowerCase())) issues.push(issue('error', `Another team is already called "${draft.name.trim()}".`))
    const before = existing ?? { ...blankTeamDraft(), members: [] as TeamMember[] }
    if (existing && existing.name !== draft.name) items.push({ tone: 'change', label: 'Rename', detail: `${existing.name} → ${draft.name}` })
    if (existing && (existing.summary ?? '') !== draft.summary) items.push({ tone: 'change', label: 'Summary changed' })
    const ids = new Set<string>()
    for (const m of draft.members) {
      const agent = ctx.agents.find((a) => a.identity.id === m.agentId)
      if (!agent) issues.push(issue('error', `Member ${m.agentId} is not a known agent.`))
      else if (agent.identity.retired) issues.push(issue('error', `${agent.identity.name} is retired and cannot join a team.`))
      else if (!agent.identity.enabled) issues.push(issue('warning', `${agent.identity.name} is disabled.`))
      if (ids.has(m.agentId)) issues.push(issue('error', `${m.agentId} appears twice.`))
      ids.add(m.agentId)
      if (!m.role.trim()) issues.push(issue('error', `${m.agentId} needs a role.`))
      const prev = before.members.find((b) => b.agentId === m.agentId)
      if (!prev) items.push({ tone: 'add', label: `${m.agentId} joins`, detail: m.role })
      else if (prev.role !== m.role) items.push({ tone: 'change', label: `${m.agentId} role`, detail: `${prev.role} → ${m.role}` })
      if (agent && PERM_RANK(agent.policy.permissionMode) > PERM_RANK(draft.policy.permissionCeiling)) issues.push(issue('warning', `${agent.identity.name} runs with ${agent.policy.permissionMode} permissions, above the team ceiling (${draft.policy.permissionCeiling}).`))
      if (agent) for (const s of agent.policy.scopes) if (!draft.policy.scopesCeiling.includes(s)) issues.push(issue('warning', `${agent.identity.name} holds scope ${s}, which is outside the team scope ceiling.`))
    }
    for (const b of before.members.filter((x) => !ids.has(x.agentId))) items.push({ tone: 'remove', label: `${b.agentId} leaves`, detail: b.role })
    if (draft.members.length && !draft.members.some((m) => m.role === 'coordinator')) issues.push(issue('warning', 'No member is a coordinator.'))
    if (draft.policy.reportTo && !draft.members.some((m) => m.agentId === draft.policy.reportTo) && !ctx.agents.some((a) => a.identity.id === draft.policy.reportTo)) issues.push(issue('error', `Report-to agent ${draft.policy.reportTo} is not known.`))
    const bp = before.policy
    const np = draft.policy
    if (np.permissionCeiling !== bp.permissionCeiling) {
      const up = PERM_RANK(np.permissionCeiling) > PERM_RANK(bp.permissionCeiling)
      items.push({ tone: 'change', label: 'Permission ceiling', detail: `${bp.permissionCeiling} → ${np.permissionCeiling}` })
      if (up && np.permissionCeiling === 'bypass') risks.push(flag('team-ceiling', 'high', 'Raises the team permission ceiling to bypass', 'Members may run tools without asking.'))
    }
    for (const s of np.scopesCeiling.filter((x) => !bp.scopesCeiling.includes(x))) {
      items.push({ tone: 'add', label: `Scope ceiling + ${s}` })
      risks.push(flag(`scope-${s}`, HIGH_SCOPES.includes(s) ? 'high' : 'medium', `Allows scope ${s} team-wide`, 'Members may be granted this scope.'))
    }
    for (const s of bp.scopesCeiling.filter((x) => !np.scopesCeiling.includes(x))) items.push({ tone: 'remove', label: `Scope ceiling - ${s}` })
    for (const r of np.rules.filter((x) => !bp.rules.includes(x))) items.push({ tone: 'add', label: `Team rule + ${r}` })
    for (const r of bp.rules.filter((x) => !np.rules.includes(x))) {
      items.push({ tone: 'remove', label: `Team rule - ${r}` })
      risks.push(flag(`rule-${r}`, GUARD_RULES.includes(r) ? 'high' : 'medium', `Removes shared rule ${r}`, GUARD_RULES.includes(r) ? 'A team-wide guard is being lifted.' : 'A team-wide constraint is being lifted.'))
    }
    if ((bp.reportTo ?? '') !== (np.reportTo ?? '')) items.push({ tone: 'change', label: 'Report-to', detail: `${bp.reportTo || '(none)'} → ${np.reportTo || '(none)'}` })
    if (existing && items.length === 0) issues.push(issue('error', 'No changes to plan.'))
    if (existing && items.some((i) => i.tone === 'remove' && i.label.endsWith('leaves'))) risks.push(flag('members-leave', 'medium', 'Removes members', 'They lose the shared policy and the team inbox.'))
    if (risks.length === 0) risks.push(flag('team-low', 'low', req.op === 'team.create' ? 'Creates a team' : 'Updates a team', 'No privileged change.'))
  }
  const op = req.op === 'team.create' ? 'Create' : req.op === 'team.delete' ? 'Delete' : 'Update'
  return assemble({ req, subjectId: req.teamId, area: 'team.write', summary: `${op} team ${label}`, draft: blankDraft(), risks, issues, items, baseRevision: req.baseRevision ?? existing?.revision ?? '' }, meta)
}

const PERM_RANK = (m: string) => (m === 'bypass' ? 2 : m === 'default' ? 1 : 0)

function planAssignments(req: Extract<LifecyclePlanRequest, { op: 'assignment.update' }>, ctx: PlanContext, meta: M): AgentPlan {
  const issues: ValidationIssue[] = []
  const items: LifecycleItem[] = []
  const risks: RiskFlag[] = []
  const touched = new Set<string>()
  const keyOf = (a: { agentId: string; target: AssignmentTarget }) => `${a.agentId}|${a.target.type}|${a.target.id}`
  const seen = new Set(ctx.assignments.map(keyOf))
  for (const id of req.remove) {
    const a = ctx.assignments.find((x) => x.id === id)
    if (!a) { issues.push(issue('error', `Assignment ${id} no longer exists.`)); continue }
    touched.add(a.agentId)
    seen.delete(keyOf(a))
    items.push({ tone: 'remove', label: `Release ${a.agentId} from ${a.target.type} ${a.target.id}`, detail: a.target.label })
    const agent = ctx.agents.find((x) => x.identity.id === a.agentId)
    if (agent?.live && agent.live.state !== 'offline' && agent.live.sessions > 0) risks.push(flag(`live-${a.id}`, 'medium', `${agent.identity.name} is running`, `Releasing ${a.target.id} may interrupt work in progress.`))
  }
  for (const d of req.add) {
    const agent = ctx.agents.find((x) => x.identity.id === d.agentId)
    if (!agent) issues.push(issue('error', `Unknown agent ${d.agentId}.`))
    else if (agent.identity.retired) issues.push(issue('error', `${agent.identity.name} is retired.`))
    else if (!agent.identity.enabled) issues.push(issue('warning', `${agent.identity.name} is disabled; the assignment waits until it is enabled.`))
    if (!d.target.id.trim()) issues.push(issue('error', 'Target id is required.'))
    if (seen.has(keyOf(d))) issues.push(issue('error', `${d.agentId} is already assigned to ${d.target.type} ${d.target.id}.`))
    seen.add(keyOf(d))
    touched.add(d.agentId)
    items.push({ tone: 'add', label: `Assign ${d.agentId} to ${d.target.type} ${d.target.id}`, detail: [d.target.label, d.role].filter(Boolean).join(' · ') || undefined })
  }
  if (items.length === 0) issues.push(issue('error', 'No changes to plan.'))
  if (risks.length === 0) risks.push(flag('assign-low', 'low', 'Changes assignments', 'No privileged change.'))
  const ids = [...touched]
  return assemble({ req, subjectId: ids.length === 1 ? ids[0] : 'assignments', area: 'assignment.write', summary: `${req.add.length} assigned, ${req.remove.length} released`, draft: blankDraft(), risks, issues, items, baseRevision: '', scopes: ids.map((i) => `agent:${i}`) }, meta)
}
