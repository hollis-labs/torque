import type { AgentsClient } from './client'
import { filterAgents } from './filter'
import { isGranted, type ConfirmResult, type ConfirmWho } from '../seams/identity-confirm'
import { applyDraft, computePlan, hasErrors, type AgentPlan, type ApplyOutcome, type AuditEntry, type AuditKind } from './plan'
import { computeLifecyclePlan, computeRetireImpact, recordFromDraft, type AgentTemplate, type AssignmentRecord, type LifecyclePlanRequest, type PlanContext, type TeamRecord } from './lifecycle'
import { MOCK_ASSIGNMENTS, MOCK_TEAMS, MOCK_TEMPLATES } from './lifecycle-fixtures'
import type { AgentRecord } from './types'

const NOW = Date.parse('2026-10-16T05:00:00Z')

/** Demo catalog. Replace by the HTTP client; shapes here are guesses, see BACKEND_NEEDS.md. */
export const MOCK_AGENTS: AgentRecord[] = [
  {
    identity: { id: 'portfolio-manager', name: 'Portfolio Manager', urn: 'msg://agent/agent-mux/agt_pm01', roles: ['partner', 'coordinator'], summary: 'Standing conversation partner across projects. Reads, drafts, recommends.', enabled: true },
    runtime: { provider: 'claude-code', model: 'claude-opus', project: 'agent-os', worker: 'agent-os', workspaceMode: 'hybrid' },
    policy: { permissionMode: 'default', sandbox: 'workspace', network: true, scopes: ['message.write'], rules: ['not-implementer', 'no-secrets', 'ask-outward'] },
    tools: ['torque', 'tesseract', 'tangent'],
    skills: ['search-first', 'capture-decision', 'capture-handoff', 'end-of-session'],
    teams: [{ teamId: 'tm_core', teamName: 'Core', role: 'coordinator' }],
    source: { path: 'catalog/agents/portfolio-manager.yaml', revision: 'a41c9e2', updatedAt: NOW - 3 * 86400_000 },
    live: { state: 'busy', sessions: 1, lastActivity: NOW - 40_000 },
  },
  {
    identity: { id: 'team-worker', name: 'Team Worker', urn: 'msg://agent/agent-mux/agt_tw02', roles: ['worker'], summary: 'Does one task in its own worktree, opens a PR, reports to a coordinator.', enabled: true },
    runtime: { provider: 'claude-code', model: 'claude-sonnet', project: 'torque', worker: 'hub', workspaceMode: 'worktree' },
    policy: { permissionMode: 'bypass', sandbox: 'workspace', network: true, scopes: [], rules: ['no-secrets', 'no-main-push', 'branch-pr', 'report-to-coordinator'] },
    tools: ['torque', 'tesseract', 'playwright', 'shadcn'],
    skills: ['search-first', 'capture-handoff'],
    teams: [{ teamId: 'tm_core', teamName: 'Core', role: 'worker' }, { teamId: 'tm_gui', teamName: 'Console GUI', role: 'worker' }],
    source: { path: 'catalog/agents/team-worker.yaml', revision: 'a41c9e2', updatedAt: NOW - 9 * 86400_000 },
    live: { state: 'waiting', sessions: 2, lastActivity: NOW - 5 * 60_000 },
  },
  {
    identity: { id: 'team-reviewer', name: 'Team Reviewer', roles: ['reviewer'], summary: 'Read-only. Fresh eyes on a diff, findings ranked by severity.', enabled: true },
    runtime: { provider: 'codex-app-server', model: 'gpt-5', project: 'torque', worker: 'hub', workspaceMode: 'worktree' },
    policy: { permissionMode: 'default', sandbox: 'workspace', network: false, scopes: [], rules: ['read-only', 'no-secrets', 'no-self-approve'] },
    tools: ['torque', 'tesseract', 'playwright', 'shadcn'],
    skills: ['search-first'],
    teams: [{ teamId: 'tm_core', teamName: 'Core', role: 'reviewer' }],
    source: { path: 'catalog/agents/team-reviewer.yaml', revision: '77bd0a1', updatedAt: NOW - 20 * 86400_000 },
    live: { state: 'idle', sessions: 1, lastActivity: NOW - 3 * 3600_000 },
  },
  {
    identity: { id: 'designer', name: 'Designer', roles: ['designer', 'partner'], summary: 'Design review and prototyping with the browser room.', enabled: true },
    runtime: { provider: 'claude-code', model: 'claude-opus', project: 'design-lab', worker: 'macbook', workspaceMode: 'hybrid' },
    policy: { permissionMode: 'default', sandbox: 'workspace', network: true, scopes: [], rules: ['no-secrets', 'ask-outward'] },
    tools: ['tangent', 'playwright', 'shadcn'],
    skills: ['search-first', 'surface-discovery'],
    teams: [],
    source: { path: 'catalog/agents/designer.yaml', revision: '12fe8c0', updatedAt: NOW - 30 * 86400_000 },
    live: { state: 'offline', sessions: 0, lastActivity: null },
  },
  {
    identity: { id: 'lead-proxy', name: 'Lead Proxy', roles: ['proxy'], summary: 'Relays messages between the operator and a lead. Decides nothing.', enabled: true },
    runtime: { provider: 'opencode', project: 'tether', worker: 'agent-os' },
    policy: { permissionMode: 'default', scopes: ['message.write'], rules: ['relay-only', 'no-secrets', 'ask-outward'] },
    tools: ['torque', 'tesseract'],
    skills: ['mailbox'],
    teams: [{ teamId: 'tm_gui', teamName: 'Console GUI', role: 'proxy' }],
    source: { path: 'catalog/agents/lead-proxy.yaml' },
  },
  {
    identity: { id: 'release-captain', name: 'Release Captain', roles: ['coordinator'], summary: 'Cuts releases and drives deploys. Disabled until the pipeline is wired.', enabled: false },
    runtime: { provider: 'claude-code', model: 'claude-sonnet', project: 'torque', worker: 'hub', workspaceMode: 'worktree' },
    policy: { permissionMode: 'bypass', sandbox: 'unrestricted', network: true, scopes: ['session.write', 'catalog.write'], rules: ['no-secrets', 'ask-outward'] },
    tools: ['torque', 'tesseract', 'playwright', 'shadcn'],
    skills: ['search-first', 'delivery-continuity', 'namespace-guard'],
    teams: [],
    source: { path: 'catalog/agents/release-captain.yaml', revision: 'c3d20ff', updatedAt: NOW - 86400_000 },
  },
]

export interface MockAgentsOptions {
  now?: () => number
  /** Agent ids whose apply fails (to exercise failure and retry UI). */
  failApplyFor?: string[]
  /** Pretend someone else changed these agents after the plan was made. */
  conflictFor?: string[]
  approvalTtlMs?: number
  /** B3: seed teams, assignments and templates (defaults to the demo fixtures). */
  teams?: TeamRecord[]
  assignments?: AssignmentRecord[]
  templates?: AgentTemplate[]
}

const OPERATOR: ConfirmWho = { id: 'mock-user', label: 'Mock User' }

/** In-memory client. Holds its own copy of the catalog so applying never mutates MOCK_AGENTS. */
export function createMockAgentsClient(agents: AgentRecord[] = MOCK_AGENTS, delayMs = 0, opts: MockAgentsOptions = {}): AgentsClient {
  const wait = () => (delayMs > 0 ? new Promise<void>((r) => setTimeout(r, delayMs)) : Promise.resolve())
  const now = opts.now ?? Date.now
  const store = new Map(agents.map((a) => [a.identity.id, structuredClone(a)]))
  const plans = new Map<string, AgentPlan>()
  const snapshots = new Map<string, AgentRecord>()
  const teams = new Map((opts.teams ?? MOCK_TEAMS).map((t) => [t.id, structuredClone(t)]))
  let assignments: AssignmentRecord[] = structuredClone(opts.assignments ?? MOCK_ASSIGNMENTS)
  const templates = opts.templates ?? MOCK_TEMPLATES
  /** Whole-state snapshots for lifecycle plans, so rollback restores agents, teams and assignments together. */
  const opSnaps = new Map<string, { agents: AgentRecord[]; teams: TeamRecord[]; assignments: AssignmentRecord[] }>()
  const planCtx = (): PlanContext => ({ agents: [...store.values()], teams: [...teams.values()], assignments })
  const syncMemberships = () => {
    for (const a of store.values()) a.teams = [...teams.values()].flatMap((t) => { const m = t.members.find((x) => x.agentId === a.identity.id); return m ? [{ teamId: t.id, teamName: t.name, role: m.role }] : [] })
  }
  const audit: AuditEntry[] = []
  let seq = 0
  const failApply = new Set(opts.failApplyFor ?? [])
  const conflict = new Set(opts.conflictFor ?? [])

  const record = (plan: AgentPlan, kind: AuditKind, actor: ConfirmWho, summary: string, extra: Partial<AuditEntry> = {}) => {
    const e: AuditEntry = { id: `aud_${++seq}`, agentId: plan.agentId, planId: plan.id, kind, at: now(), actor, summary, risk: plan.risk, changeCount: plan.changes.length, ...extra }
    audit.push(e)
    return e
  }
  // The real backend verifies the token; the mock checks what it can see.
  const check = (c: ConfirmResult, action: string, plan: AgentPlan) => {
    if (!isGranted(c, now())) throw new Error(`Step-up not granted (${c.outcome})`)
    if (c.what.action !== action) throw new Error(`Step-up was for "${c.what.action}", not "${action}"`)
    const missing = plan.requiredScope.filter((s) => !c.scope.includes(s))
    if (missing.length) throw new Error(`Step-up does not cover scope: ${missing.join(', ')}`)
  }
  const need = (id: string) => {
    const p = plans.get(id)
    if (!p) throw new Error(`Unknown plan ${id}`)
    return p
  }
  const confInfo = (c: ConfirmResult) => ({ outcome: c.outcome, scope: c.scope, issuedAt: c.issuedAt, expiresAt: c.expiresAt })
  const save = (p: AgentPlan, patch: Partial<AgentPlan>) => {
    const next = { ...p, ...patch }
    plans.set(p.id, next)
    return next
  }
  let rev = 0

  const applyLifecycle = (plan: AgentPlan, c: ConfirmResult): ApplyOutcome => {
    const req = plan.lifecycle!.request
    const info = confInfo(c)
    const t = now()
    const conflict = (msg: string, before = ''): ApplyOutcome => {
      const next = save(plan, { status: 'conflict' })
      record(next, 'apply_failed', c.who, `Conflict: ${msg}`, { revisionBefore: before, confirmation: info })
      return { status: 'conflict', plan: next, revisionBefore: before, message: 'The target changed since this plan was made. Re-plan from the current version.' }
    }
    switch (req.op) {
      case 'agent.create': if (store.has(req.agentId)) return conflict(`${req.agentId} now exists`); break
      case 'agent.retire':
      case 'agent.restore': { const a = store.get(req.agentId); if (!a || (a.source.revision ?? '') !== plan.baseRevision) return conflict(`${req.agentId} changed`, a?.source.revision) ; break }
      case 'team.create': if (teams.has(req.teamId)) return conflict(`${req.teamId} now exists`); break
      case 'team.update':
      case 'team.delete': { const tm = teams.get(req.teamId); if (!tm || tm.revision !== plan.baseRevision) return conflict(`${req.teamId} changed`, tm?.revision); break }
      case 'assignment.update': if (req.remove.some((id) => !assignments.some((a) => a.id === id))) return conflict('an assignment was already released'); break
    }
    if (failApply.has(plan.agentId)) {
      failApply.delete(plan.agentId)
      const next = save(plan, { status: 'failed' })
      const a = record(next, 'apply_failed', c.who, 'Write failed; nothing changed', { confirmation: info })
      return { status: 'failed', plan: next, auditId: a.id, message: 'The catalog rejected the write. Nothing changed.' }
    }
    opSnaps.set(plan.id, { agents: structuredClone([...store.values()]), teams: structuredClone([...teams.values()]), assignments: structuredClone(assignments) })
    const revision = `m${++rev}${String(plan.id).slice(-2)}`
    let agent: AgentRecord | undefined
    let team: TeamRecord | undefined
    let before = ''
    switch (req.op) {
      case 'agent.create':
        agent = recordFromDraft(req.agentId, req.name, req.draft, { now: t, revision })
        store.set(agent.identity.id, agent)
        break
      case 'agent.retire': {
        const a = store.get(req.agentId)!
        before = a.source.revision ?? ''
        for (const tm of teams.values()) if (tm.members.some((m) => m.agentId === a.identity.id)) teams.set(tm.id, { ...tm, members: tm.members.filter((m) => m.agentId !== req.agentId), revision: `t${++rev}`, updatedAt: t })
        assignments = assignments.filter((x) => x.agentId !== req.agentId)
        const live = a.live && req.sessions === 'stop' ? { ...a.live, state: 'offline' as const, sessions: 0 } : a.live
        agent = { ...a, identity: { ...a.identity, enabled: false, retired: true, retiredAt: t }, live, source: { ...a.source, revision, updatedAt: t } }
        store.set(req.agentId, agent)
        syncMemberships()
        break
      }
      case 'agent.restore': {
        const a = store.get(req.agentId)!
        before = a.source.revision ?? ''
        agent = { ...a, identity: { ...a.identity, enabled: false, retired: false, retiredAt: null }, source: { ...a.source, revision, updatedAt: t } }
        store.set(req.agentId, agent)
        break
      }
      case 'team.create':
      case 'team.update':
        before = teams.get(req.teamId)?.revision ?? ''
        team = { id: req.teamId, ...structuredClone(req.team), name: req.team.name.trim(), revision: `t${++rev}`, updatedAt: t }
        teams.set(req.teamId, team)
        syncMemberships()
        break
      case 'team.delete':
        before = teams.get(req.teamId)?.revision ?? ''
        teams.delete(req.teamId)
        syncMemberships()
        break
      case 'assignment.update':
        assignments = [
          ...assignments.filter((a) => !req.remove.includes(a.id)),
          ...req.add.map((d) => ({ id: `asg_${++seq}`, agentId: d.agentId, target: { ...d.target }, role: d.role, since: t })),
        ]
        break
    }
    const next = save(plan, { status: 'applied' })
    const a = record(next, 'apply', c.who, `Applied: ${plan.lifecycle!.summary}`, { revisionBefore: before, revisionAfter: team?.revision ?? revision, confirmation: info })
    return { status: 'applied', plan: next, agent, team, revisionBefore: before, revisionAfter: team?.revision ?? revision, auditId: a.id }
  }

  const rollbackLifecycle = (plan: AgentPlan, c: ConfirmResult): ApplyOutcome => {
    const snap = opSnaps.get(plan.id)
    if (plan.status !== 'applied' || !snap) throw new Error('Nothing to roll back')
    check(c, 'agent.plan.rollback', plan)
    store.clear()
    snap.agents.forEach((a) => store.set(a.identity.id, a))
    teams.clear()
    snap.teams.forEach((x) => teams.set(x.id, x))
    assignments = snap.assignments
    opSnaps.delete(plan.id)
    const next = save(plan, { status: 'rolled_back' })
    const a = record(next, 'rollback', c.who, `Rolled back: ${plan.lifecycle!.summary}`, { confirmation: confInfo(c) })
    const req = plan.lifecycle!.request
    return { status: 'rolled_back', plan: next, agent: req.op.startsWith('agent.') ? store.get((req as { agentId: string }).agentId) : undefined, auditId: a.id }
  }

  return {
    async listAgents(query) {
      await wait()
      return filterAgents([...store.values()], query)
    },
    async getAgent(id) {
      await wait()
      return store.get(id) ?? null
    },
    async planChange(req) {
      await wait()
      const agent = store.get(req.agentId)
      if (!agent) throw new Error(`Unknown agent ${req.agentId}`)
      const actor = req.actor ?? OPERATOR
      const plan = computePlan(agent, req, { id: `plan_${++seq}`, now: now(), actor })
      plans.set(plan.id, plan)
      record(plan, 'plan', actor, `Planned ${plan.changes.length} change${plan.changes.length === 1 ? '' : 's'}`)
      return plan
    },
    async getPlan(id) {
      await wait()
      return plans.get(id) ?? null
    },
    async approvePlan(planId, confirmation) {
      await wait()
      const plan = need(planId)
      if (plan.status !== 'proposed') throw new Error(`Plan is ${plan.status}; only a proposed plan can be approved`)
      if (hasErrors(plan.issues)) throw new Error('Plan has validation errors')
      check(confirmation, 'agent.plan.approve', plan)
      const approval = { by: confirmation.who, at: now(), expiresAt: now() + (opts.approvalTtlMs ?? 15 * 60_000), scope: confirmation.scope }
      const next = save(plan, { status: 'approved', approval })
      record(next, 'approve', confirmation.who, 'Approved', { confirmation: confInfo(confirmation) })
      return next
    },
    async applyPlan(planId, confirmation) {
      await wait()
      const plan = need(planId)
      if (plan.status !== 'approved' || !plan.approval) throw new Error(`Plan is ${plan.status}; approve it first`)
      check(confirmation, 'agent.plan.apply', plan)
      if (plan.approval.expiresAt <= now()) {
        const next = save(plan, { status: 'expired' })
        return { status: 'expired', plan: next, message: 'Approval expired. Approve the plan again.' }
      }
      if (plan.lifecycle) return applyLifecycle(plan, confirmation)
      const agent = store.get(plan.agentId)
      if (!agent) throw new Error(`Unknown agent ${plan.agentId}`)
      const before = agent.source.revision ?? ''
      if (conflict.has(plan.agentId) || before !== plan.baseRevision) {
        const next = save(plan, { status: 'conflict' })
        record(next, 'apply_failed', confirmation.who, `Conflict: definition is at ${before || 'unknown'}, plan was made at ${plan.baseRevision || 'unknown'}`, { revisionBefore: before, confirmation: confInfo(confirmation) })
        return { status: 'conflict', plan: next, revisionBefore: before, message: 'The definition changed since this plan was made. Re-plan from the current version.' }
      }
      if (failApply.has(plan.agentId)) {
        failApply.delete(plan.agentId)
        const next = save(plan, { status: 'failed' })
        const a = record(next, 'apply_failed', confirmation.who, 'Write failed; definition unchanged', { revisionBefore: before, confirmation: confInfo(confirmation) })
        return { status: 'failed', plan: next, revisionBefore: before, auditId: a.id, message: 'The catalog rejected the write. The definition is unchanged.' }
      }
      snapshots.set(plan.id, structuredClone(agent))
      const after = `m${++rev}${String(plan.id).slice(-2)}`
      const updated: AgentRecord = { ...applyDraft(agent, plan.draft), source: { ...agent.source, revision: after, updatedAt: now() } }
      store.set(plan.agentId, updated)
      const next = save(plan, { status: 'applied' })
      const a = record(next, 'apply', confirmation.who, `Applied ${plan.changes.length} change${plan.changes.length === 1 ? '' : 's'}`, { revisionBefore: before, revisionAfter: after, confirmation: confInfo(confirmation) })
      return { status: 'applied', plan: next, agent: updated, revisionBefore: before, revisionAfter: after, auditId: a.id }
    },
    async rollbackApply(planId, confirmation) {
      await wait()
      const plan = need(planId)
      if (plan.lifecycle) return rollbackLifecycle(plan, confirmation)
      const snap = snapshots.get(planId)
      if (plan.status !== 'applied' || !snap) throw new Error('Nothing to roll back')
      check(confirmation, 'agent.plan.rollback', plan)
      const cur = store.get(plan.agentId)!
      const last = [...audit].reverse().find((e) => e.planId === planId && e.kind === 'apply')
      if (last?.revisionAfter && cur.source.revision !== last.revisionAfter) {
        return { status: 'conflict', plan, message: 'The definition changed after this plan was applied; roll back manually.' }
      }
      const after = `m${++rev}r`
      const restored: AgentRecord = { ...snap, source: { ...snap.source, revision: after, updatedAt: now() } }
      store.set(plan.agentId, restored)
      const next = save(plan, { status: 'rolled_back' })
      const a = record(next, 'rollback', confirmation.who, 'Rolled back', { revisionBefore: cur.source.revision, revisionAfter: after, confirmation: confInfo(confirmation) })
      return { status: 'rolled_back', plan: next, agent: restored, revisionBefore: cur.source.revision, revisionAfter: after, auditId: a.id } satisfies ApplyOutcome
    },
    async planLifecycle(req: LifecyclePlanRequest) {
      await wait()
      const actor = req.actor ?? OPERATOR
      const plan = computeLifecyclePlan(req, planCtx(), { id: `plan_${++seq}`, now: now(), actor })
      plans.set(plan.id, plan)
      record(plan, 'plan', actor, `Planned: ${plan.lifecycle?.summary ?? 'change'}`)
      return plan
    },
    async getRetireImpact(agentId) {
      await wait()
      const a = store.get(agentId)
      if (!a) throw new Error(`Unknown agent ${agentId}`)
      return computeRetireImpact(a, planCtx())
    },
    async listTemplates() {
      await wait()
      return structuredClone(templates)
    },
    async listTeams() {
      await wait()
      return structuredClone([...teams.values()]).sort((a, b) => a.name.localeCompare(b.name))
    },
    async getTeam(id) {
      await wait()
      const t = teams.get(id)
      return t ? structuredClone(t) : null
    },
    async listAssignments(q) {
      await wait()
      return structuredClone(assignments.filter((a) => (!q?.agentId || a.agentId === q.agentId) && (!q?.targetId || a.target.id === q.targetId)))
    },
    async listAudit(agentId) {
      await wait()
      return audit.filter((e) => e.agentId === agentId).sort((a, b) => b.at - a.at || b.id.localeCompare(a.id, undefined, { numeric: true }))
    },
  }
}


