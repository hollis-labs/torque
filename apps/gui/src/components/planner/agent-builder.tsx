import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { AlertTriangle, Check, ChevronLeft, ChevronRight, Copy, Dices, Lock, ShieldAlert, Sparkles, Undo2 } from 'lucide-react'
import { Button, Input } from '@hollis-labs/sysop-ui'
import { PixelRobot } from '@/components/planner/agent-activity'
import {
  ARCHETYPES,
  PERMISSION_MODES,
  PROJECTS,
  WORKSPACE_MODES,
  WORKERS,
  effectiveServers,
  planCommand,
  planFiles,
  PROVIDERS,
  RULES,
  SANDBOXES,
  SCOPES,
  SKILLS,
  STEPS,
  archetypeOf,
  authority,
  authorityLabel,
  avatarSeed,
  buildDefinition,
  draftFromArchetype,
  elevations,
  renderCharter,
  slugify,
  systemPrompt,
  toYaml,
  validate,
  type Draft,
  type Risk,
  type StepId,
} from '@/lib/agent-builder'

// Slugs already in the (demo) catalog, so collisions are visible.
const TAKEN = ['general', 'designer', 'frontend', 'team-worker', 'team-reviewer', 'lead-proxy', 'portfolio-manager']

const RISK_TEXT: Record<Risk, string> = { low: 'text-sky-300', medium: 'text-amber-300', high: 'text-red-300' }
const RISK_BORDER: Record<Risk, string> = { low: 'border-sky-400/60', medium: 'border-amber-400/60', high: 'border-red-400/70' }

function Tile({ on, onClick, children, tone = 'border-emerald-400/70 bg-emerald-400/10', disabled, className = '' }: { on: boolean; onClick: () => void; children: ReactNode; tone?: string; disabled?: boolean; className?: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      disabled={disabled}
      onClick={onClick}
      className={`border-2 px-3 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed ${on ? tone : 'border-zinc-800 bg-zinc-950/60 hover:border-zinc-600'} ${className}`}
    >
      {children}
    </button>
  )
}

const Label = ({ children }: { children: ReactNode }) => <h3 className="mb-1.5 font-mono text-[10px] uppercase tracking-widest text-zinc-500">{children}</h3>

/** Ten stepped cells; fills and recolours as authority grows. */
function PowerMeter({ value }: { value: number }) {
  const cells = 10
  const lit = Math.round((value / 100) * cells)
  const label = authorityLabel(value)
  const color = label === 'MILD' ? 'bg-sky-400' : label === 'STANDARD' ? 'bg-emerald-400' : label === 'ELEVATED' ? 'bg-amber-400' : 'bg-red-400'
  const text = label === 'MILD' ? 'text-sky-300' : label === 'STANDARD' ? 'text-emerald-300' : label === 'ELEVATED' ? 'text-amber-300' : 'text-red-300'
  return (
    <div role="meter" aria-label="Authority" aria-valuemin={0} aria-valuemax={100} aria-valuenow={value} aria-valuetext={label}>
      <div className="flex gap-0.5">
        {Array.from({ length: cells }, (_, i) => (
          <span key={i} className={`h-3 flex-1 transition-colors duration-300 ${i < lit ? color : 'bg-zinc-800'}`} />
        ))}
      </div>
      <div className={`mt-1 flex justify-between font-mono text-[10px] uppercase tracking-widest ${text}`}>
        <span>authority</span>
        <span>{label}</span>
      </div>
    </div>
  )
}

function IdentityStep({ d, set }: { d: Draft; set: (patch: Partial<Draft>) => void }) {
  const setName = (name: string) => set({ name, ...(d.slugEdited ? {} : { slug: slugify(name) }), ...(d.charterEdited ? {} : { charter: renderCharter(archetypeOf(d.archetype), name) }) })
  return (
    <div className="space-y-6">
      <section>
        <Label>Pick a class</Label>
        <div className="grid gap-2 sm:grid-cols-2">
          {ARCHETYPES.map((a) => (
            <Tile key={a.id} on={d.archetype === a.id} onClick={() => set(draftFromArchetype(a.id, d))} tone="border-amber-300/80 bg-amber-300/10">
              <span className="block font-mono text-sm uppercase tracking-wide">{a.label}</span>
              <span className="mt-0.5 block text-[12px] text-muted-foreground">{a.blurb}</span>
            </Tile>
          ))}
        </div>
      </section>
      <section className="grid gap-4 sm:grid-cols-2">
        <label className="block">
          <Label>Name</Label>
          <Input value={d.name} onChange={(e) => setName(e.target.value)} placeholder="Release Captain" autoFocus />
        </label>
        <label className="block">
          <Label>Slug {d.slugEdited ? '' : '· follows the name'}</Label>
          <Input value={d.slug} onChange={(e) => set({ slug: slugify(e.target.value), slugEdited: true })} placeholder="release-captain" className="font-mono" />
        </label>
      </section>
      <p className="font-mono text-[11px] text-zinc-500">urn · <span className="text-zinc-400">msg://agent/agent-mux/agt_…</span> assigned once, on apply</p>
      <section>
        <Label>Roles · how teams find it</Label>
        <div className="flex flex-wrap gap-2">
          {[...new Set([...d.roles, 'worker', 'reviewer', 'partner', 'designer', 'coordinator'])].map((r) => (
            <Tile key={r} on={d.roles.includes(r)} onClick={() => set({ roles: d.roles.includes(r) ? d.roles.filter((x) => x !== r) : [...d.roles, r] })} className="py-1 font-mono text-[11px] uppercase tracking-wider">
              {r}
            </Tile>
          ))}
        </div>
      </section>
    </div>
  )
}

function CharterStep({ d, set }: { d: Draft; set: (patch: Partial<Draft>) => void }) {
  const [showPrompt, setShowPrompt] = useState(false)
  return (
    <div className="space-y-6">
      <section>
        <Label>Hard rules · in force every session</Label>
        <div className="grid gap-2 sm:grid-cols-2">
          {RULES.map((r) => {
            const on = d.rules.includes(r.id)
            return (
              <Tile key={r.id} on={on} disabled={r.locked} onClick={() => set({ rules: on ? d.rules.filter((x) => x !== r.id) : [...d.rules, r.id] })}>
                <span className="flex items-center gap-1.5 font-mono text-[12px] uppercase tracking-wide">
                  {r.locked && <Lock className="h-3 w-3 text-zinc-500" aria-label="Always on" />}
                  {r.label}
                </span>
                <span className="mt-0.5 block text-[12px] text-muted-foreground">{r.text}</span>
              </Tile>
            )
          })}
        </div>
      </section>
      <section>
        <div className="flex items-end justify-between">
          <Label>Charter · how it works with you</Label>
          {d.charterEdited && (
            <button type="button" className="mb-1.5 font-mono text-[10px] uppercase tracking-widest text-amber-300 hover:underline" onClick={() => set({ charter: renderCharter(archetypeOf(d.archetype), d.name), charterEdited: false })}>
              reset to class
            </button>
          )}
        </div>
        <textarea
          value={d.charter}
          onChange={(e) => set({ charter: e.target.value, charterEdited: true })}
          rows={9}
          aria-label="Charter"
          className="w-full border-2 border-zinc-800 bg-zinc-950 p-3 font-mono text-[12px] leading-relaxed focus-visible:border-zinc-500 focus-visible:outline-none"
        />
      </section>
      <section>
        <button type="button" onClick={() => setShowPrompt((v) => !v)} className="font-mono text-[10px] uppercase tracking-widest text-zinc-400 hover:text-zinc-200" aria-expanded={showPrompt}>
          {showPrompt ? '▾' : '▸'} compiled system prompt
        </button>
        {showPrompt && <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap border border-border bg-zinc-950 p-3 font-mono text-[11px] text-zinc-400">{systemPrompt(d)}</pre>}
      </section>
    </div>
  )
}

function SkillsStep({ d, set }: { d: Draft; set: (patch: Partial<Draft>) => void }) {
  return (
    <section>
      <Label>Skill deck · {d.skills.length} equipped</Label>
      <div className="grid gap-2 sm:grid-cols-2">
        {SKILLS.map((s) => {
          const on = d.skills.includes(s.id)
          return (
            <Tile key={s.id} on={on} onClick={() => set({ skills: on ? d.skills.filter((x) => x !== s.id) : [...d.skills, s.id] })}>
              <span className="flex items-center justify-between font-mono text-[12px] uppercase tracking-wide">
                {s.id}
                {on && <Check className="h-3.5 w-3.5 text-emerald-300" aria-label="Equipped" />}
              </span>
              <span className="mt-0.5 block text-[12px] text-muted-foreground">{s.blurb}</span>
            </Tile>
          )
        })}
      </div>
    </section>
  )
}

const Privileged = () => <span className="border border-amber-400/60 px-1 font-mono text-[9px] uppercase tracking-widest text-amber-300">privileged</span>

function AccessStep({ d, set }: { d: Draft; set: (patch: Partial<Draft>) => void }) {
  const servers = effectiveServers(d.project)
  const noTorque = !servers.includes('torque')
  return (
    <div className="space-y-6">
      <section>
        <Label>Home project · decides which tools it can call</Label>
        <div className="flex flex-wrap gap-2">
          {PROJECTS.map((p) => (
            <Tile key={p} on={d.project === p} onClick={() => set({ project: p })} className="py-1 font-mono text-[11px] uppercase tracking-wider">
              {p}
            </Tile>
          ))}
        </div>
        <div className="mt-3 border border-border p-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">tools via {d.project}</span>
            {servers.map((s) => (
              <span key={s} className="border border-emerald-400/50 bg-emerald-400/10 px-1.5 py-0.5 font-mono text-[11px] uppercase tracking-wider text-emerald-300">{s}</span>
            ))}
          </div>
          <p className="mt-2 text-[12px] text-muted-foreground">
            Tools come from the project, not the agent. A project list replaces the default (torque only), so a project that leaves torque out gives none. To change it, edit the project.
          </p>
          {noTorque && <p className="mt-1 flex items-center gap-1.5 text-[12px] text-amber-300"><AlertTriangle className="h-3 w-3" /> This project does not grant torque.</p>}
        </div>
      </section>
      <section>
        <Label>Tether scopes · power cells · <Privileged /></Label>
        <div className="grid gap-2 sm:grid-cols-2">
          {SCOPES.map((s) => {
            const on = d.scopes.includes(s.id)
            return (
              <Tile key={s.id} on={on} tone={`${RISK_BORDER[s.risk]} bg-zinc-900`} onClick={() => set({ scopes: on ? d.scopes.filter((x) => x !== s.id) : [...d.scopes, s.id] })}>
                <span className="flex items-center justify-between font-mono text-[12px] uppercase tracking-wide">
                  {s.label}
                  <span className={`text-[9px] tracking-widest ${RISK_TEXT[s.risk]}`}>{s.risk}</span>
                </span>
                <span className="mt-0.5 block text-[12px] text-muted-foreground">
                  {on ? 'Grants: ' : ''}{s.blurb} · <span className="font-mono">{s.id}</span>
                </span>
              </Tile>
            )
          })}
        </div>
      </section>
      <section>
        <Label>Tool permission · <Privileged /></Label>
        <div className="grid gap-2 sm:grid-cols-2">
          {PERMISSION_MODES.map((m) => (
            <Tile key={m.id} on={d.permissionMode === m.id} tone={`${RISK_BORDER[m.risk]} bg-zinc-900`} onClick={() => set({ permissionMode: m.id as Draft['permissionMode'] })}>
              <span className="block font-mono text-[12px] uppercase tracking-wide">{m.label}</span>
              <span className="block text-[12px] text-muted-foreground">{m.blurb}</span>
            </Tile>
          ))}
        </div>
        <p className="mt-1.5 text-[11px] text-zinc-500">Only “bypass” is confirmed in the live catalog; the other value is a guess to confirm.</p>
      </section>
      <section className="grid gap-4 sm:grid-cols-2">
        <div>
          <Label>Network · <Privileged /></Label>
          <Tile on={d.network} onClick={() => set({ network: !d.network })} className="w-full">
            <span className="block font-mono text-[12px] uppercase tracking-wide">{d.network ? 'Online' : 'Offline'}</span>
            <span className="block text-[12px] text-muted-foreground">{d.network ? 'Can reach the network' : 'No network access'}</span>
          </Tile>
        </div>
        <div>
          <Label>Sandbox · field name unconfirmed</Label>
          <div className="grid gap-2">
            {SANDBOXES.map((s) => (
              <Tile key={s.id} on={d.sandbox === s.id} tone={s.id === 'unrestricted' ? 'border-red-400/70 bg-zinc-900' : 'border-emerald-400/70 bg-emerald-400/10'} onClick={() => set({ sandbox: s.id as Draft['sandbox'] })}>
                <span className="block font-mono text-[12px] uppercase tracking-wide">{s.label}</span>
                <span className="block text-[12px] text-muted-foreground">{s.blurb}</span>
              </Tile>
            ))}
          </div>
        </div>
      </section>
    </div>
  )
}

function RunStep({ d, set }: { d: Draft; set: (patch: Partial<Draft>) => void }) {
  return (
    <div className="space-y-6">
      <section>
        <Label>Engine · a pick-list; providers are infrastructure</Label>
        <div className="grid gap-2 sm:grid-cols-2">
          {PROVIDERS.map((p) => (
            <Tile key={p.id} on={d.provider === p.id} onClick={() => set({ provider: p.id })}>
              <span className="block font-mono text-[12px] uppercase tracking-wide">{p.label}</span>
              <span className="block font-mono text-[11px] text-zinc-500">{p.id} · {p.runtime}</span>
            </Tile>
          ))}
        </div>
      </section>
      <section className="grid gap-4 sm:grid-cols-2">
        <div>
          <Label>Workspace</Label>
          <div className="grid gap-2">
            {WORKSPACE_MODES.map((m) => (
              <Tile key={m.id} on={d.workspaceMode === m.id} onClick={() => set({ workspaceMode: m.id as Draft['workspaceMode'] })}>
                <span className="block font-mono text-[12px] uppercase tracking-wide">{m.label}</span>
                <span className="block text-[12px] text-muted-foreground">{m.blurb}</span>
              </Tile>
            ))}
          </div>
        </div>
        <div>
          <Label>Worker machine</Label>
          <div className="flex flex-wrap gap-2">
            {WORKERS.map((w) => (
              <Tile key={w} on={d.worker === w} onClick={() => set({ worker: w })} className="py-1 font-mono text-[11px] uppercase tracking-wider">
                {w}
              </Tile>
            ))}
          </div>
        </div>
      </section>
      <section>
        <Label>Talks and reports</Label>
        <div className="grid gap-2 sm:grid-cols-3">
          <Tile on={d.chat} onClick={() => set({ chat: !d.chat })}>
            <span className="block font-mono text-[12px] uppercase tracking-wide">Chat</span>
            <span className="block text-[12px] text-muted-foreground">You can talk to it</span>
          </Tile>
          <Tile on={d.routeFinal} onClick={() => set({ routeFinal: !d.routeFinal })}>
            <span className="block font-mono text-[12px] uppercase tracking-wide">Final → inbox</span>
            <span className="block text-[12px] text-muted-foreground">Route results</span>
          </Tile>
          <Tile on={d.routeFailure} onClick={() => set({ routeFailure: !d.routeFailure })}>
            <span className="block font-mono text-[12px] uppercase tracking-wide">Failure → inbox</span>
            <span className="block text-[12px] text-muted-foreground">Route failures</span>
          </Tile>
        </div>
        <Tile on={d.enabled} onClick={() => set({ enabled: !d.enabled })} className="mt-2 w-full">
          <span className="block font-mono text-[12px] uppercase tracking-wide">{d.enabled ? 'Enabled' : 'Disabled'}</span>
          <span className="block text-[12px] text-muted-foreground">Launching is a separate, deliberate act after this is applied.</span>
        </Tile>
      </section>
    </div>
  )
}

const FLOW = ['Validate', 'Plan', 'Approve at a terminal', 'Apply', 'Done'] as const
const PLAN_ID = 'plan_7f3a91c2'

function ReviewStep({ d, issues, set }: { d: Draft; issues: ReturnType<typeof validate>; set: (patch: Partial<Draft>) => void }) {
  const need = elevations(d)
  const blocked = issues.some((i) => i.level === 'error')
  const files = useMemo(() => planFiles(d), [d])
  const [phase, setPhase] = useState(0) // 0 idle; 1 validating; 2 planning; 3 waiting for terminal approval; 4 applying; 5 applied
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (phase !== 1 && phase !== 2 && phase !== 4) return
    const t = setTimeout(() => setPhase((p) => p + 1), 700)
    return () => clearTimeout(t)
  }, [phase])

  return (
    <div className="space-y-6">
      <section>
        <Label>Audit · every catalog change is tied to a Torque task</Label>
        <Input value={d.taskId} onChange={(e) => set({ taskId: e.target.value })} placeholder="CW-20261015-0001" className="max-w-xs font-mono" disabled={phase !== 0} aria-label="Torque task id" />
      </section>

      <section>
        <Label>Plan · two new files</Label>
        {files.map((f) => (
          <div key={f.path} className="mb-2 border border-border">
            <div className="border-b border-border bg-zinc-950/60 px-3 py-1 font-mono text-[11px] text-zinc-400">{f.path}</div>
            <pre className="max-h-40 overflow-auto p-2 font-mono text-[11px] leading-relaxed">
              {f.lines.map((l, i) => (
                <span key={i} className="block bg-emerald-400/5 text-emerald-300">+ {l}</span>
              ))}
            </pre>
          </div>
        ))}
        <p className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">0 running sessions use these · new agent</p>
      </section>

      {need.length > 0 && (
        <section className="border-2 border-amber-400/60 bg-amber-400/5 p-3">
          <h3 className="flex items-center gap-2 font-mono text-[11px] uppercase tracking-widest text-amber-300">
            <ShieldAlert className="h-4 w-4" /> Privileged changes · the operator approves these by plan digest
          </h3>
          <ul className="mt-2 space-y-1">
            {need.map((e) => (
              <li key={e.text} className="text-[12px]"><span className="font-mono">{e.text}</span> <span className="text-muted-foreground">— {e.grants}</span></li>
            ))}
          </ul>
        </section>
      )}

      <section>
        <Label>Safe path</Label>
        <ol className="border border-border">
          {FLOW.map((r, i) => {
            const done = phase > i + 1
            const active = phase === i + 1
            return (
              <li key={r} className="flex items-center gap-3 border-b border-border px-3 py-2 last:border-b-0">
                <span className={`flex h-5 w-5 items-center justify-center border-2 font-mono text-[10px] ${done || phase === 5 ? 'border-emerald-400 bg-emerald-400 text-zinc-950' : active ? 'agent-pulse-bar border-amber-300 text-amber-300' : 'border-zinc-700 text-zinc-600'}`}>{done || phase === 5 ? <Check className="h-3 w-3" /> : i + 1}</span>
                <span className={`font-mono text-[12px] uppercase tracking-wide ${done ? 'text-emerald-300' : active ? 'text-amber-200' : 'text-zinc-400'}`}>{r}</span>
              </li>
            )
          })}
        </ol>
        {phase === 3 && (
          <div className="mt-3 border-2 border-amber-300/70 bg-amber-300/5 p-3">
            <p className="text-[13px]">Approval happens outside this page, on purpose. Run this where you operate Tether, then this page picks it up. The plan expires in 15 minutes.</p>
            <div className="mt-2 flex items-center gap-2">
              <code className="flex-1 border border-border bg-zinc-950 px-2 py-1.5 font-mono text-[12px]">{planCommand(PLAN_ID)}</code>
              <Button size="sm" variant="outline" onClick={() => { void navigator.clipboard?.writeText(planCommand(PLAN_ID)); setCopied(true) }} className="gap-1">
                <Copy className="h-3.5 w-3.5" /> {copied ? 'Copied' : 'Copy'}
              </Button>
            </div>
            <button type="button" onClick={() => setPhase(4)} className="mt-2 font-mono text-[10px] uppercase tracking-widest text-zinc-400 hover:text-zinc-200">demo only: pretend I ran it</button>
          </div>
        )}
        {phase === 5 && (
          <div className="mt-3 space-y-2 border-2 border-emerald-400/60 bg-emerald-400/5 p-3 text-[13px]">
            <p><span className="font-mono">backup_id</span> bkp_20261015_0612 · activation <span className="font-mono">immediate</span> · audit note posted on {d.taskId}</p>
            <p className="font-mono text-[10px] uppercase tracking-widest text-emerald-300">demo: nothing was written</p>
            <Button size="sm" variant="outline" className="gap-1" onClick={() => setPhase(0)}><Undo2 className="h-3.5 w-3.5" /> Plan a rollback</Button>
          </div>
        )}
      </section>

      <div className="flex flex-wrap items-center gap-3">
        <Button disabled={blocked || phase !== 0} onClick={() => setPhase(1)} className="gap-1.5">
          <Sparkles className="h-3.5 w-3.5" /> Validate &amp; plan
        </Button>
        {blocked && <span className="font-mono text-[11px] uppercase tracking-widest text-red-300">fix the errors first</span>}
      </div>
    </div>
  )
}

function SpecSheet({ d, issues, onJump }: { d: Draft; issues: ReturnType<typeof validate>; onJump: (s: StepId) => void }) {
  const def = useMemo(() => buildDefinition(d), [d])
  const power = authority(d)
  const seed = avatarSeed(d)
  return (
    <aside aria-label="Spec sheet" className="space-y-4 border-zinc-800 lg:sticky lg:top-0 lg:border-l-2 lg:pl-5">
      <div className="flex items-center gap-4">
        <span className="shrink-0 border-2 border-zinc-600 bg-zinc-950 p-2">
          <PixelRobot id={seed} size={88} state="idle" />
        </span>
        <div className="min-w-0">
          <div className="truncate font-mono text-base font-semibold uppercase tracking-wide">{d.name.trim() || 'Unnamed'}</div>
          <div className="truncate font-mono text-[10px] uppercase tracking-wider text-zinc-500">{d.roles.join(' · ') || 'no role'}</div>
        </div>
      </div>
      <PowerMeter value={power} />
      {issues.length > 0 && (
        <ul aria-label="Checks" className="space-y-1">
          {issues.map((i, k) => (
            <li key={k}>
              <button type="button" onClick={() => onJump(i.step)} className={`flex w-full items-start gap-2 text-left text-[12px] hover:underline ${i.level === 'error' ? 'text-red-300' : 'text-amber-300'}`}>
                <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" /> {i.text}
              </button>
            </li>
          ))}
        </ul>
      )}
      <div>
        <Label>Live definition</Label>
        <pre className="max-h-72 overflow-auto border border-border bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed text-zinc-300">{toYaml(def.agent)}{'\n---\n'}{toYaml(def.launch)}</pre>
      </div>
    </aside>
  )
}

export function AgentBuilderView() {
  const [d, setD] = useState<Draft>(() => draftFromArchetype('partner'))
  const [step, setStep] = useState(0)
  const set = (patch: Partial<Draft>) => setD((prev) => ({ ...prev, ...patch }))
  const issues = useMemo(() => validate(d, TAKEN), [d])
  const id = STEPS[step].id
  // Only call a step wrong once the user has moved past it; a fresh form should not start red.
  const bad = (s: StepId) => STEPS.findIndex((x) => x.id === s) < step && issues.some((i) => i.step === s && i.level === 'error')
  const warn = (s: StepId) => STEPS.findIndex((x) => x.id === s) < step && issues.some((i) => i.step === s)

  return (
    <div className="agent-arcade mx-auto max-w-6xl px-4 py-4">
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <p className="text-[13px] text-muted-foreground">Build an agent as you would a character: pick a class, equip skills, set its access, choose how it runs, then plan it for approval.</p>
        <span className="ml-auto font-mono text-[10px] uppercase tracking-widest text-zinc-500">design spike · demo data · nothing is written</span>
      </div>
      <div className="grid gap-6 lg:grid-cols-[11rem_minmax(0,1fr)_20rem]">
        <nav aria-label="Builder steps" className="flex gap-1 overflow-x-auto lg:flex-col">
          {STEPS.map((s, i) => {
            const here = i === step
            const done = i < step && !bad(s.id)
            return (
              <button
                key={s.id}
                type="button"
                onClick={() => setStep(i)}
                aria-current={here ? 'step' : undefined}
                className={`flex shrink-0 items-center gap-3 border-2 px-3 py-2 text-left transition-colors ${here ? 'border-amber-300/80 bg-amber-300/10' : 'border-transparent hover:border-zinc-700'}`}
              >
                <span className={`flex h-6 w-6 shrink-0 items-center justify-center font-mono text-[11px] ${bad(s.id) ? 'bg-red-400 text-zinc-950' : done ? 'bg-emerald-400 text-zinc-950' : here ? 'bg-amber-300 text-zinc-950' : 'bg-zinc-800 text-zinc-400'}`}>
                  {done ? <Check className="h-3.5 w-3.5" /> : i + 1}
                </span>
                <span>
                  <span className="block font-mono text-[12px] uppercase tracking-wide">{s.label}</span>
                  <span className="hidden text-[11px] text-zinc-500 lg:block">{s.hint}</span>
                </span>
                {warn(s.id) && !bad(s.id) && <span className="ml-auto hidden h-1.5 w-1.5 bg-amber-400 lg:block" aria-label="Has warnings" />}
              </button>
            )
          })}
        </nav>

        <main className="min-w-0">
          <h2 className="mb-4 flex items-center gap-3 font-mono text-lg uppercase tracking-wide">
            {STEPS[step].label}
            {id === 'identity' && (
              <button type="button" onClick={() => set({ variant: d.variant + 1 })} className="inline-flex items-center gap-1.5 border-2 border-zinc-700 px-2 py-1 text-[10px] tracking-widest text-zinc-300 hover:border-zinc-500" aria-label="Reroll avatar">
                <Dices className="h-3.5 w-3.5" /> reroll look
              </button>
            )}
          </h2>
          {id === 'identity' && <IdentityStep d={d} set={set} />}
          {id === 'charter' && <CharterStep d={d} set={set} />}
          {id === 'skills' && <SkillsStep d={d} set={set} />}
          {id === 'access' && <AccessStep d={d} set={set} />}
          {id === 'run' && <RunStep d={d} set={set} />}
          {id === 'review' && <ReviewStep d={d} issues={issues} set={set} />}
          <div className="mt-8 flex items-center gap-2 border-t border-border pt-4">
            <Button variant="outline" size="sm" disabled={step === 0} onClick={() => setStep((s) => s - 1)} className="gap-1">
              <ChevronLeft className="h-3.5 w-3.5" /> Back
            </Button>
            <Button size="sm" disabled={step === STEPS.length - 1} onClick={() => setStep((s) => s + 1)} className="ml-auto gap-1">
              {STEPS[step + 1]?.label ?? 'Done'} <ChevronRight className="h-3.5 w-3.5" />
            </Button>
          </div>
        </main>

        <SpecSheet d={d} issues={issues} onJump={(s) => setStep(STEPS.findIndex((x) => x.id === s))} />
      </div>
    </div>
  )
}
