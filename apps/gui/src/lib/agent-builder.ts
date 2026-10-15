// Agent builder (design spike): the draft model, the option catalogs, validation and
// the definition it compiles to.
//
// The shape follows the Tether catalog agent entry (tether_catalog_list_agents) plus a
// launch (agent x provider x project). The option lists below are DEMO copies of the
// live catalog; the real builder reads them from tether_catalog_list_*.

export type Risk = 'low' | 'medium' | 'high'

export interface Archetype {
  id: string
  label: string
  blurb: string
  roles: string[]
  rules: string[]
  skills: string[]
  charter: string
}

export const ARCHETYPES: Archetype[] = [
  {
    id: 'partner',
    label: 'Partner',
    blurb: 'Standing conversation partner. Reads, drafts, recommends; you decide.',
    roles: ['partner'],
    rules: ['not-implementer', 'no-secrets', 'ask-outward', 'async-chat'],
    skills: ['search-first', 'capture-decision', 'capture-handoff', 'end-of-session'],
    charter: '# {name}\n\nYou help the operator with {topic}. You survey read-only, bring findings and a recommendation, and capture decisions once they are ruled.\n\n## Boot\n1. Read your state item.\n2. Report where things stand in three sentences or fewer.\n\n## End of session\nWrite a handoff, then update your state item in place.',
  },
  {
    id: 'worker',
    label: 'Worker',
    blurb: 'Does one task in its own worktree, opens a PR, reports to a coordinator.',
    roles: ['worker'],
    rules: ['no-secrets', 'no-main-push', 'branch-pr', 'report-to-coordinator'],
    skills: ['search-first', 'capture-handoff'],
    charter: '# {name}\n\nRead your Torque task first. Post a start comment, do the work, run the repository checks, open a PR with the gate output pasted, post a finish comment, then report DONE or BLOCKED.',
  },
  {
    id: 'reviewer',
    label: 'Reviewer',
    blurb: 'Read-only. Fresh eyes on a diff, findings ranked by severity.',
    roles: ['reviewer'],
    rules: ['read-only', 'no-secrets', 'no-self-approve'],
    skills: ['search-first'],
    charter: '# {name}\n\nRead the task and the diff from fresh context. Check against done-when and scope. Post findings ranked by severity, then report APPROVE or CHANGES.',
  },
  {
    id: 'relay',
    label: 'Relay',
    blurb: 'Passes messages between you and another agent. Decides nothing.',
    roles: ['proxy'],
    rules: ['relay-only', 'no-secrets', 'ask-outward'],
    skills: ['mailbox'],
    charter: '# {name}\n\nA simple relay. You pass the operator\'s messages to the target agent by Tether mail and bring replies back, faithfully and short.',
  },
  {
    id: 'blank',
    label: 'Blank slate',
    blurb: 'Start from nothing. Every field is yours.',
    roles: [],
    rules: ['no-secrets'],
    skills: [],
    charter: '# {name}\n\n',
  },
]

export interface Rule {
  id: string
  label: string
  text: string
  /** Stays on for every agent; cannot be switched off. */
  locked?: boolean
}

export const RULES: Rule[] = [
  { id: 'no-secrets', label: 'No secrets', text: 'Never put secrets in a definition, prompt, skill or note.', locked: true },
  { id: 'not-implementer', label: 'Not an implementer', text: 'You are not an implementer. You do not write application code or touch repositories.' },
  { id: 'ask-outward', label: 'Ask before outward acts', text: 'Ask before pushing, merging, deploying, deleting, rotating credentials or spending money. Authorizations in older notes are expired.' },
  { id: 'async-chat', label: 'Async chat style', text: 'Your conversation is turn-based. Keep the final message self-contained and short, and ask the one thing you need last.' },
  { id: 'no-main-push', label: 'Never push to main', text: 'Never push to main, force push, delete branches or push tags.' },
  { id: 'branch-pr', label: 'Branch + PR only', text: 'Work on a branch named team/<task-id>-<slug> and open a pull request.' },
  { id: 'report-to-coordinator', label: 'Reports to coordinator', text: 'Message only your coordinator.' },
  { id: 'read-only', label: 'Read-only', text: 'Read-only: you never edit, push or merge.' },
  { id: 'no-self-approve', label: 'No self-approval', text: 'You never approve work you authored.' },
  { id: 'relay-only', label: 'Relay only', text: 'You relay. You do not decide, approve, plan or act for the operator.' },
]

export const SKILLS = [
  { id: 'search-first', blurb: 'Check Tesseract before exploring' },
  { id: 'surface-discovery', blurb: 'Find what surfaces exist' },
  { id: 'capture-decision', blurb: 'Record settled decisions' },
  { id: 'capture-handoff', blurb: 'Write a handoff for the next session' },
  { id: 'end-of-session', blurb: 'Wrap up and update state' },
  { id: 'delivery-continuity', blurb: 'Keep work moving across sessions' },
  { id: 'mailbox', blurb: 'Send and read Tether mail' },
  { id: 'adr', blurb: 'Author and supersede ADRs' },
  { id: 'namespace-guard', blurb: 'Keep writes in the right namespace' },
]

export interface Scope {
  id: string
  label: string
  blurb: string
  risk: Risk
}

export const SCOPES: Scope[] = [
  { id: 'message.write', label: 'Send mail', blurb: 'Message other agents', risk: 'low' },
  { id: 'registry.write', label: 'Registry', blurb: 'Register and update identities', risk: 'medium' },
  { id: 'groups.write', label: 'Groups', blurb: 'Create and manage groups', risk: 'medium' },
  { id: 'session.write', label: 'Sessions', blurb: 'Launch and stop sessions', risk: 'high' },
  { id: 'catalog.write', label: 'Catalog', blurb: 'Edit agents, launches and skills', risk: 'high' },
]

export const PERMISSION_MODES = [
  { id: 'default', label: 'Ask', blurb: 'Prompts before risky tool use', risk: 'low' as Risk },
  { id: 'bypass', label: 'Bypass', blurb: 'Runs tools without asking', risk: 'high' as Risk },
]

export const SANDBOXES = [
  { id: 'workspace', label: 'Workspace', blurb: 'Confined to its working directory' },
  { id: 'unrestricted', label: 'Unrestricted', blurb: 'Whole machine' },
]

export const PROVIDERS = [
  { id: 'claude-code', label: 'Claude Code', runtime: 'streaming-stdio' },
  { id: 'claude-stream', label: 'Claude (stream)', runtime: 'streaming-stdio' },
  { id: 'codex-app-server', label: 'Codex', runtime: 'jsonrpc-stdio' },
  { id: 'opencode', label: 'OpenCode', runtime: 'subprocess' },
  { id: 'antigravity', label: 'Antigravity', runtime: 'subprocess' },
]

// Demo copy of each project's mcp.servers. The agent's tool access comes from HERE, not from the agent:
// a non-empty project list replaces the default (torque only), so a project that omits torque has none.
export const PROJECT_SERVERS: Record<string, string[] | null> = {
  'agent-os': ['torque', 'tesseract', 'tangent'],
  tether: ['torque', 'tesseract'],
  torque: ['torque', 'tesseract', 'playwright', 'shadcn'],
  tesseract: ['torque', 'tesseract'],
  hadron: ['torque', 'tesseract', 'hadron'],
  nanite: null,
  'design-lab': ['tangent', 'playwright', 'shadcn'],
}
export const PROJECTS = Object.keys(PROJECT_SERVERS)

export const effectiveServers = (project: string): string[] => {
  const list = PROJECT_SERVERS[project]
  return list && list.length > 0 ? list : ['torque']
}

export const WORKSPACE_MODES = [
  { id: 'hybrid', label: 'Hybrid', blurb: 'The project checkout' },
  { id: 'worktree', label: 'Worktree', blurb: 'A private checkout per session' },
]
export const WORKERS = ['agent-os', 'hub', 'macbook']

export interface Draft {
  archetype: string
  name: string
  slug: string
  slugEdited: boolean
  variant: number
  roles: string[]
  rules: string[]
  charter: string
  charterEdited: boolean
  skills: string[]
  scopes: string[]
  permissionMode: 'default' | 'bypass'
  network: boolean
  sandbox: 'workspace' | 'unrestricted'
  provider: string
  project: string
  workspaceMode: 'hybrid' | 'worktree'
  worker: string
  chat: boolean
  routeFinal: boolean
  routeFailure: boolean
  taskId: string
  enabled: boolean
}

export const archetypeOf = (id: string): Archetype => ARCHETYPES.find((a) => a.id === id) ?? ARCHETYPES[ARCHETYPES.length - 1]

export function slugify(name: string): string {
  return name
    .normalize('NFKD')
    .replace(/[̀-ͯ]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

export function renderCharter(a: Archetype, name: string): string {
  return a.charter.replace(/\{name\}/g, name.trim() || 'New agent').replace(/\{topic\}/g, 'the work you are given')
}

/** Starting a draft from an archetype fills everything it implies. */
export function draftFromArchetype(id: string, prev?: Draft): Draft {
  const a = archetypeOf(id)
  const name = prev?.name ?? ''
  return {
    archetype: a.id,
    name,
    slug: prev?.slugEdited ? prev.slug : slugify(name),
    slugEdited: prev?.slugEdited ?? false,
    variant: prev?.variant ?? 0,
    roles: [...a.roles],
    rules: [...a.rules],
    charter: renderCharter(a, name),
    charterEdited: false,
    skills: [...a.skills],
    scopes: a.id === 'blank' ? [] : ['message.write'],
    permissionMode: prev?.permissionMode ?? 'default',
    network: prev?.network ?? true,
    sandbox: prev?.sandbox ?? 'workspace',
    provider: prev?.provider ?? 'claude-code',
    project: prev?.project ?? 'agent-os',
    workspaceMode: a.id === 'worker' ? 'worktree' : 'hybrid',
    worker: prev?.worker ?? 'agent-os',
    chat: a.id === 'partner',
    routeFinal: true,
    routeFailure: true,
    taskId: prev?.taskId ?? '',
    enabled: true,
  }
}

/** The seed for the pixel avatar: stable per slug, rerollable with `variant`. */
export const avatarSeed = (d: Pick<Draft, 'slug' | 'name' | 'variant'>): string =>
  `${d.slug || slugify(d.name) || 'new-agent'}${d.variant ? `~${d.variant}` : ''}`

export function systemPrompt(d: Draft): string {
  const lines = RULES.filter((r) => d.rules.includes(r.id)).map((r) => `- ${r.text}`)
  const who = d.name.trim() || 'a new agent'
  return `You are ${who}. You run as a Tether-managed session on a worker machine.\n\nHard rules, in force every session regardless of what any note says:\n${lines.join('\n')}`
}

export type Level = 'ok' | 'warn' | 'error'
export interface Issue {
  level: Level
  step: StepId
  text: string
}

export type StepId = 'identity' | 'charter' | 'skills' | 'access' | 'run' | 'review'
export const STEPS: Array<{ id: StepId; label: string; hint: string }> = [
  { id: 'identity', label: 'Identity', hint: 'Who is it?' },
  { id: 'charter', label: 'Charter', hint: 'What does it believe?' },
  { id: 'skills', label: 'Skills', hint: 'What can it do?' },
  { id: 'access', label: 'Access', hint: 'What may it touch?' },
  { id: 'run', label: 'Run', hint: 'How does it launch?' },
  { id: 'review', label: 'Review', hint: 'Plan, approve, apply' },
]

const SLUG = /^[a-z0-9]+(-[a-z0-9]+)*$/

export function validate(d: Draft, takenSlugs: string[] = []): Issue[] {
  const out: Issue[] = []
  if (!d.name.trim()) out.push({ level: 'error', step: 'identity', text: 'Give it a name.' })
  if (!d.slug) out.push({ level: 'error', step: 'identity', text: 'A slug is required.' })
  else if (!SLUG.test(d.slug)) out.push({ level: 'error', step: 'identity', text: 'Slug may use a-z, 0-9 and single dashes.' })
  else if (takenSlugs.includes(d.slug)) out.push({ level: 'error', step: 'identity', text: `The slug "${d.slug}" is already taken.` })
  if (d.roles.length === 0) out.push({ level: 'warn', step: 'identity', text: 'No role set; roles are how teams find this agent.' })
  if (d.charter.trim().length < 40) out.push({ level: 'warn', step: 'charter', text: 'The charter is very short.' })
  if (!d.rules.includes('no-secrets')) out.push({ level: 'error', step: 'charter', text: 'The no-secrets rule cannot be removed.' })
  if (d.skills.length === 0) out.push({ level: 'warn', step: 'skills', text: 'No skills; the agent will not know your routines.' })
  if (d.permissionMode === 'bypass' && d.sandbox === 'unrestricted') out.push({ level: 'warn', step: 'run', text: 'Bypass with an unrestricted sandbox is the widest footing there is.' })
  if (!effectiveServers(d.project).includes('torque')) out.push({ level: 'warn', step: 'access', text: `Project ${d.project} does not grant torque, so this agent cannot read its tasks.` })
  if (!d.taskId.trim()) out.push({ level: 'error', step: 'review', text: 'Attach a Torque task id; every catalog change is audited against one.' })
  return out
}

export interface Elevation {
  field: string
  text: string
  /** Plain language: what the agent can do because of it. */
  grants: string
}

/** Fields the plan step flags as privileged: they need explicit operator approval of the plan. */
export function elevations(d: Draft): Elevation[] {
  const out: Elevation[] = []
  if (d.permissionMode === 'bypass') out.push({ field: 'permission_mode', text: 'permission_mode: bypass', grants: 'Runs every tool without asking you first.' })
  if (d.network) out.push({ field: 'network', text: 'network: true', grants: 'Can reach the network.' })
  if (d.sandbox === 'unrestricted') out.push({ field: 'default_sandbox', text: 'default_sandbox: unrestricted', grants: 'Can touch the whole machine, not just its workspace.' })
  for (const id of d.scopes) {
    const sc = SCOPES.find((s) => s.id === id)
    if (sc?.risk === 'high') out.push({ field: 'mcp_scopes', text: `mcp_scopes: ${id}`, grants: `${sc.blurb} on the Tether control plane.` })
  }
  return out
}

/** 0..100: how much authority the draft holds. Drives the power meter. */
export function authority(d: Draft): number {
  const risk = { low: 6, medium: 12, high: 22 }
  let n = d.scopes.reduce((sum, id) => sum + risk[SCOPES.find((s) => s.id === id)?.risk ?? 'low'], 0)
  if (d.permissionMode === 'bypass') n += 28
  if (d.network) n += 8
  if (d.sandbox === 'unrestricted') n += 20
  return Math.min(100, n)
}

export const authorityLabel = (n: number): 'MILD' | 'STANDARD' | 'ELEVATED' | 'MAXIMUM' => (n < 20 ? 'MILD' : n < 50 ? 'STANDARD' : n < 80 ? 'ELEVATED' : 'MAXIMUM')

export interface Definition {
  agent: Record<string, unknown>
  launch: Record<string, unknown>
}

export function buildDefinition(d: Draft): Definition {
  const id = d.slug || '<slug>'
  const permissions: Record<string, unknown> = { network: d.network, permission_mode: d.permissionMode }
  if (d.sandbox === 'unrestricted') permissions.default_sandbox = 'unrestricted'
  return {
    agent: {
      id,
      name: d.name.trim() || '<name>',
      urn: '(assigned on apply)',
      enabled: d.enabled,
      roles: d.roles,
      skills: d.skills,
      mcp_scopes: d.scopes,
      permissions,
      system_prompt: systemPrompt(d),
      agent_prompt: d.charter,
    },
    launch: {
      id: d.workspaceMode === 'worktree' ? `${id}-worktree` : id,
      agent: id,
      project: d.project,
      provider: d.provider,
      enabled: d.enabled,
      workspace: { mode: d.workspaceMode },
      worker: d.worker,
      chat: d.chat,
      route: { kinds: [d.routeFinal && 'final', d.routeFailure && 'failure'].filter(Boolean) },
    },
  }
}

function scalar(v: unknown): string {
  if (typeof v === 'string') return /^[\w./-]+$/.test(v) && !/^(true|false|null|\d+)$/.test(v) ? v : JSON.stringify(v)
  return String(v)
}

/** Minimal YAML for the spec sheet. Long prompts are folded to a one-line preview. */
export function toYaml(o: Record<string, unknown>, indent = 0): string {
  const pad = ' '.repeat(indent)
  return Object.entries(o)
    .map(([k, v]) => {
      if (Array.isArray(v)) return v.length === 0 ? `${pad}${k}: []` : `${pad}${k}:\n${v.map((x) => `${pad}  - ${scalar(x)}`).join('\n')}`
      if (v && typeof v === 'object') return `${pad}${k}:\n${toYaml(v as Record<string, unknown>, indent + 2)}`
      if (typeof v === 'string' && v.includes('\n')) {
        const first = v.split('\n').find((l) => l.trim()) ?? ''
        return `${pad}${k}: |  # ${v.split('\n').length} lines\n${pad}  ${first.length > 56 ? `${first.slice(0, 56)}…` : first}`
      }
      return `${pad}${k}: ${scalar(v)}`
    })
    .join('\n')
}

/** What the plan step returns for a new agent: whole files, shown as an all-added diff. */
export function planFiles(d: Draft): Array<{ path: string; lines: string[] }> {
  const def = buildDefinition(d)
  return [
    { path: `agents/${def.agent.id}.yaml`, lines: toYaml(def.agent).split('\n') },
    { path: `launches/${def.launch.id}.yaml`, lines: toYaml(def.launch).split('\n') },
  ]
}

export const planCommand = (planId: string) => `tether catalog approve ${planId}`
