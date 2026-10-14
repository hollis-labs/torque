import type {
  PlannerBoardRow,
  PlannerBoardSectionKey,
  PlannerDb,
  PlannerFieldSchema,
  PlannerItem,
  PlannerLink,
  PlannerSchemaInfo,
} from '@/lib/types'

// Pure logic for the Planner pages: database metadata, status tones, form
// fields built from the server schema, patch building, link targets. No
// React and no network, so it is tested on inline fixtures.

export interface PlannerDbMeta {
  key: PlannerDb
  label: string
  singular: string
  prefix: string
  blurb: string
}

/** Tab order: the tracker's, with the inbox last. */
export const PLANNER_DBS: PlannerDbMeta[] = [
  { key: 'priorities', label: 'Priorities', singular: 'priority', prefix: 'PR-', blurb: 'What matters most, in the order given.' },
  { key: 'workstreams', label: 'Workstreams', singular: 'workstream', prefix: 'WS-', blurb: 'Tether workstreams with their Torque projects and current state.' },
  { key: 'roadmap', label: 'Roadmap', singular: 'roadmap item', prefix: 'RM-', blurb: 'Capabilities and milestones by area and horizon.' },
  { key: 'ideas', label: 'Ideas', singular: 'idea', prefix: 'ID-', blurb: 'Questions, explorations and things to discuss.' },
  { key: 'decisions', label: 'Decisions', singular: 'decision', prefix: 'DEC-', blurb: 'The decision log, and the decisions still open.' },
  { key: 'risks', label: 'Risks', singular: 'risk', prefix: 'RK-', blurb: 'Risks, gaps, debt and known limitations.' },
  { key: 'inbox', label: 'Inbox', singular: 'inbox item', prefix: 'IN-', blurb: 'Things to look at that are not items yet. Triage them into a database or dismiss them.' },
]

export function isPlannerDb(v: string | undefined): v is PlannerDb {
  return PLANNER_DBS.some((d) => d.key === v)
}

export function plannerMeta(db: PlannerDb): PlannerDbMeta {
  return PLANNER_DBS.find((d) => d.key === db)!
}

/** The database an id belongs to, from its prefix (DEC- before ID-/IN-). */
export function dbOfId(id: string): PlannerDb | null {
  const hit = [...PLANNER_DBS].sort((a, b) => b.prefix.length - a.prefix.length).find((d) => id.startsWith(d.prefix))
  return hit ? hit.key : null
}

// ---------- status ----------

/** The kit's status keys (see sysop-ui `statusTone`); planner statuses borrow their colors. */
export type ToneKey = 'backlog' | 'todo' | 'queued' | 'doing' | 'review' | 'done' | 'blocked' | 'paused' | 'archived'

const TONES: Record<string, ToneKey> = {
  // open work
  new: 'todo', open: 'todo', 'needs-decision': 'todo', planned: 'backlog', idea: 'backlog', queued: 'queued', unreviewed: 'backlog', triaged: 'queued',
  // in motion
  active: 'doing', 'in-progress': 'doing', discussing: 'review', exploring: 'review', mitigating: 'review', adopting: 'review',
  // settled
  decided: 'done', done: 'done', landed: 'done', promoted: 'done', accepted: 'done',
  // stopped
  deferred: 'paused', parked: 'paused', paused: 'paused', blocked: 'blocked',
  dropped: 'archived', dismissed: 'archived', closed: 'archived', superseded: 'archived', reversed: 'archived',
}

export function statusToneKey(status: string | undefined): ToneKey {
  return TONES[(status ?? '').toLowerCase()] ?? 'backlog'
}

/** Statuses that count as finished or stopped: the list hides them behind a toggle by default. */
const SETTLED = new Set(['decided', 'done', 'landed', 'promoted', 'accepted', 'dropped', 'dismissed', 'closed', 'superseded', 'reversed'])

export function isSettled(status: string | undefined): boolean {
  return SETTLED.has((status ?? '').toLowerCase())
}

export interface StatusCount {
  status: string
  count: number
}

/** Counts per status, in first-seen order. */
export function statusCounts(items: PlannerItem[]): StatusCount[] {
  const seen = new Map<string, number>()
  for (const it of items) {
    const s = it.status ?? ''
    if (s) seen.set(s, (seen.get(s) ?? 0) + 1)
  }
  return [...seen].map(([status, count]) => ({ status, count }))
}

/** Filter by status (empty = all) and a text query over id, title, tldr. */
export function filterItems(items: PlannerItem[], opts: { status?: string; query?: string }): PlannerItem[] {
  const q = (opts.query ?? '').trim().toLowerCase()
  const terms = q ? q.split(/\s+/) : []
  return items.filter((it) => {
    if (opts.status && it.status !== opts.status) return false
    if (terms.length === 0) return true
    const hay = `${it.id}\n${it.title}\n${it.tldr ?? ''}`.toLowerCase()
    return terms.every((t) => hay.includes(t))
  })
}

/** Short facts shown beside a row's title, per database. */
export function itemFacts(db: PlannerDb, it: PlannerItem): string[] {
  const s = (k: string) => (typeof it[k] === 'string' && it[k] ? (it[k] as string) : '')
  const out: string[] = []
  switch (db) {
    case 'priorities':
      if (typeof it.rank === 'number') out.push(`#${it.rank}`)
      break
    case 'workstreams':
      out.push(s('phase'), s('owner'))
      break
    case 'roadmap':
      out.push(s('area'), s('horizon'))
      if (it.mvp === true) out.push('MVP')
      break
    case 'ideas':
      out.push(s('kind'), s('discuss_in'))
      break
    case 'decisions':
      out.push(s('area'), s('date'))
      break
    case 'risks':
      out.push(s('kind'), s('severity'))
      break
    case 'inbox':
      out.push(s('kind'))
      break
  }
  return out.filter(Boolean)
}

/** arrayMove for drag reorder: the ids with `activeId` moved to where `overId` is. */
export function moveId(ids: string[], activeId: string, overId: string): string[] {
  const from = ids.indexOf(activeId)
  const to = ids.indexOf(overId)
  if (from < 0 || to < 0 || from === to) return ids
  const next = [...ids]
  next.splice(from, 1)
  next.splice(to, 0, activeId)
  return next
}

// ---------- links ----------

export interface LinkTarget {
  /** In-app route, or null. */
  to: string | null
  /** External URL (http/https only), or null. */
  href: string | null
}

/** Where a pointer leads: a Torque task opens in the app, a URL opens outside it, the rest are plain text. */
export function linkTarget(link: PlannerLink): LinkTarget {
  if (link.kind === 'torque') return { to: `/tasks/${encodeURIComponent(link.ref)}`, href: null }
  if (link.kind === 'url' && /^https?:\/\/\S+$/i.test(link.ref)) return { to: null, href: link.ref }
  return { to: null, href: null }
}

/** Client-side check mirroring the server's, so the form can say what is wrong before sending. */
export function validateLinkDraft(kind: string, ref: string): string | null {
  const r = ref.trim()
  if (!r) return 'Enter what to point at.'
  if (kind === 'url' && !/^https?:\/\/\S+$/i.test(r)) return 'A URL must start with http:// or https://.'
  if (kind === 'torque' && !/^[A-Z]{2,6}-\d{8}-\d{3,6}$/.test(r)) return 'A Torque task id looks like CW-20261003-0137.'
  return null
}

// ---------- fields (forms built from the server schema) ----------

export type FieldKind = 'text' | 'longtext' | 'enum' | 'int' | 'bool' | 'list'

export interface FieldSpec {
  name: string
  kind: FieldKind
  label: string
  required: boolean
  options?: string[]
  hint?: string
  min?: number
  max?: number
}

/** Fields handled by their own panel, or by the server; never in the generic form. */
const STRUCTURAL = new Set([
  'id', 'rev', 'created', 'updated', 'author', 'order', 'comments', 'links', 'options', 'promoted_to', 'added_by', 'source',
  'supersedes', 'superseded_by', 'selected_option', 'decided_at', 'decided_by', 'defer_note',
])

const LONG = new Set([
  'tldr', 'notes', 'summary', 'description', 'impact', 'mitigation', 'body', 'rationale', 'decision', 'context',
  'recommendation', 'goal', 'state', 'outcome', 'next_step',
])

/** The fields the edit and create forms offer, in the order the schema lists them, title first. */
export function fieldSpecs(schema: PlannerSchemaInfo): FieldSpec[] {
  const specs: FieldSpec[] = []
  for (const [name, f] of Object.entries(schema.fields)) {
    if (STRUCTURAL.has(name)) continue
    const kind = fieldKind(name, f)
    if (!kind) continue
    specs.push({
      name,
      kind,
      label: labelOf(name),
      required: schema.required.includes(name),
      options: f.enum ?? schema.enums[name],
      hint: f.description,
      min: f.minimum,
      max: f.maximum,
    })
  }
  return specs.sort((a, b) => (a.name === 'title' ? -1 : b.name === 'title' ? 1 : 0))
}

function fieldKind(name: string, f: PlannerFieldSchema): FieldKind | null {
  if (f.enum) return 'enum'
  const t = Array.isArray(f.type) ? f.type[0] : f.type
  if (t === 'array') return f.items === 'string' ? 'list' : null // arrays of objects (options, comments, links) have their own panels
  if (t === 'integer' || t === 'number') return name === 'order' ? null : 'int'
  if (t === 'boolean') return 'bool'
  if (t === 'string') return LONG.has(name) ? 'longtext' : 'text'
  return null
}

export function labelOf(name: string): string {
  const spaced = name.replace(/_ids$/, ' links').replace(/_/g, ' ')
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

/** A form draft: every value is a string (list = one entry per line, bool = 'true'/'false'). */
export type Draft = Record<string, string>

export function draftFrom(specs: FieldSpec[], item: PlannerItem | null): Draft {
  const d: Draft = {}
  for (const s of specs) {
    const v = item?.[s.name]
    if (v === undefined || v === null) d[s.name] = s.kind === 'bool' ? 'false' : ''
    else if (s.kind === 'list') d[s.name] = Array.isArray(v) ? v.map(String).join('\n') : ''
    else d[s.name] = String(v)
  }
  return d
}

function parseField(s: FieldSpec, raw: string): unknown | undefined {
  const text = raw.trim()
  switch (s.kind) {
    case 'list': {
      const lines = raw.split('\n').map((l) => l.trim()).filter(Boolean)
      return lines.length ? lines : undefined
    }
    case 'int': {
      if (!text) return undefined
      const n = Number(text)
      return Number.isFinite(n) ? Math.trunc(n) : undefined
    }
    case 'bool':
      return text === 'true' ? true : undefined
    default:
      return text === '' ? undefined : (s.kind === 'longtext' ? raw.replace(/\s+$/, '') : text)
  }
}

function sameValue(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

/**
 * The patch that turns `original` into the draft: changed fields only, with
 * null where the draft cleared a field. Empty means nothing changed.
 */
export function buildPatch(specs: FieldSpec[], original: PlannerItem, draft: Draft): Record<string, unknown> {
  const patch: Record<string, unknown> = {}
  for (const s of specs) {
    const next = parseField(s, draft[s.name] ?? '')
    const prev = original[s.name]
    if (next === undefined) {
      if (prev !== undefined && prev !== null && !(Array.isArray(prev) && prev.length === 0) && prev !== '' && prev !== false) patch[s.name] = null
    } else if (!sameValue(next, prev)) {
      patch[s.name] = next
    }
  }
  return patch
}

/** The item a create form builds: filled fields only. */
export function buildNewItem(specs: FieldSpec[], draft: Draft): Record<string, unknown> {
  const item: Record<string, unknown> = {}
  for (const s of specs) {
    const v = parseField(s, draft[s.name] ?? '')
    if (v !== undefined) item[s.name] = v
  }
  return item
}

/** Required fields a draft leaves empty, as labels. */
export function missingRequired(specs: FieldSpec[], draft: Draft): string[] {
  return specs.filter((s) => s.required && parseField(s, draft[s.name] ?? '') === undefined).map((s) => s.label)
}

// ---------- errors ----------

export interface FieldError {
  path: string
  message: string
}

/** Validation failures from a planner API error body, if it carried any. */
export function validationErrors(details: Record<string, unknown> | undefined): FieldError[] {
  const errs = details?.errors
  if (!Array.isArray(errs)) return []
  return errs.filter((e): e is FieldError => !!e && typeof (e as FieldError).path === 'string' && typeof (e as FieldError).message === 'string')
}

/** The item a conflict says is current, if the error carried it. */
export function conflictCurrent(details: Record<string, unknown> | undefined): PlannerItem | null {
  const cur = details?.current
  return cur && typeof cur === 'object' && typeof (cur as PlannerItem).id === 'string' ? (cur as PlannerItem) : null
}

// ---------- create defaults ----------

/** What the server fills in when a create omits it (mirrors PlannerService defaults). */
const SERVER_DEFAULTS: Record<PlannerDb, Record<string, string>> = {
  priorities: { status: 'queued' },
  workstreams: { status: 'planned' },
  roadmap: { status: 'idea' },
  ideas: { status: 'new' },
  decisions: { status: 'needs-decision' },
  risks: { status: 'open' },
  inbox: { status: 'new', kind: 'topic' },
}

/**
 * The fields a create form shows: what the schema requires, plus tldr. The
 * rest are edited after the item exists.
 */
export function createSpecs(schema: PlannerSchemaInfo): FieldSpec[] {
  return fieldSpecs(schema).filter((s) => s.required || s.name === 'tldr')
}

/** A starting draft for a create form: server defaults, else the first option of a required choice, else the next rank. */
export function createDraft(db: PlannerDb, specs: FieldSpec[], nextRank: number): Draft {
  const d = draftFrom(specs, null)
  for (const s of specs) {
    if (s.kind === 'enum') d[s.name] = SERVER_DEFAULTS[db][s.name] ?? (s.required ? s.options?.[0] ?? '' : '')
    if (s.name === 'rank' && s.kind === 'int') d[s.name] = String(nextRank)
  }
  return d
}

// ---------- board ----------

export const BOARD_SECTIONS: Array<{ key: PlannerBoardSectionKey; title: string; empty: string; blurb: string }> = [
  { key: 'in_flight', title: 'In flight', empty: 'Nothing is being worked right now.', blurb: 'Actively worked, and work arriving for review.' },
  { key: 'landed', title: 'Recently landed', empty: 'Nothing has landed in the window.', blurb: 'Finished tasks, landed roadmap items and decisions made.' },
  { key: 'pre_flight', title: 'Pre-flight', empty: 'Nothing is queued.', blurb: 'Preparing, not executing: queued, quiet, boarding, planned for now or next.' },
  { key: 'holds', title: 'Holds', empty: 'Nothing is waiting on you.', blurb: 'Decisions to make, open high risks, and an inbox to triage.' },
]

/** The curated board's sections: the same four keys, worded for the workstreams we manage. */
export const CURATED_SECTIONS: Array<{ key: PlannerBoardSectionKey; title: string; empty: string; blurb: string }> = [
  { key: 'in_flight', title: 'In motion', empty: 'No board workstream is active.', blurb: 'Workstreams tagged board that are active.' },
  { key: 'pre_flight', title: 'Next up', empty: 'No board workstream is planned.', blurb: 'Workstreams tagged board that are planned or paused.' },
  { key: 'landed', title: 'Recently landed', empty: 'Nothing has landed in the window.', blurb: 'Board workstreams finished in the window.' },
  { key: 'holds', title: 'Waiting on you', empty: 'Nothing is waiting on you.', blurb: 'Open decisions, risks and inbox items around the board, and workstreams with blockers.' },
]

/** "2 doing, 5 todo": a workstream's Torque task roll-up. */
export function taskCountsText(counts?: Record<string, number>): string {
  return Object.entries(counts ?? {})
    .filter(([, n]) => n > 0)
    .map(([status, n]) => `${n} ${status}`)
    .join(', ')
}

/** The label a board row carries (what the departure board calls it), and the kit tone it borrows. */
export function boardWord(section: PlannerBoardSectionKey, row: PlannerBoardRow): { word: string; tone: ToneKey } {
  if (section === 'holds' && row.source === 'workstream') return { word: 'BLOCKED', tone: 'blocked' }
  if (row.source === 'workstream') {
    const WS: Record<string, { word: string; tone: ToneKey }> = {
      active: { word: 'ACTIVE', tone: 'doing' },
      planned: { word: 'PLANNED', tone: 'queued' },
      paused: { word: 'PAUSED', tone: 'paused' },
      done: { word: 'LANDED', tone: 'done' },
    }
    return WS[row.status] ?? { word: row.status.toUpperCase(), tone: statusToneKey(row.status) }
  }
  if (section === 'holds') {
    const hold = { decision: 'DECIDE', inbox: 'TRIAGE', risk: 'RISK' }[row.source as 'decision' | 'inbox' | 'risk'] ?? 'HOLD'
    return { word: hold, tone: row.source === 'risk' ? 'blocked' : 'todo' }
  }
  if (section === 'pre_flight' && row.status === 'doing') return { word: 'PREPARING', tone: 'paused' }
  const WORDS: Record<string, { word: string; tone: ToneKey }> = {
    doing: { word: 'IN FLIGHT', tone: 'doing' },
    'in-progress': { word: 'IN FLIGHT', tone: 'doing' },
    review: { word: 'ARRIVING', tone: 'review' },
    done: { word: 'LANDED', tone: 'done' },
    landed: { word: 'LANDED', tone: 'done' },
    decided: { word: 'LANDED', tone: 'done' },
    queued: { word: 'QUEUED', tone: 'queued' },
    planned: { word: 'QUEUED', tone: 'queued' },
    adopting: { word: 'BOARDING', tone: 'review' },
  }
  return WORDS[row.status] ?? { word: row.status.toUpperCase(), tone: statusToneKey(row.status) }
}

/**
 * Where a board row leads: back to the board with the item open in a modal
 * (`?open=<db>/<id>` for a planner item, `?task=<id>` for a Torque task), so
 * closing returns to the board and the view can be linked.
 */
export function boardRowRoute(row: PlannerBoardRow, base = '/planner/board'): string {
  if (row.source === 'torque' || !row.db) return `${base}?task=${encodeURIComponent(row.id)}`
  return `${base}?open=${row.db}/${encodeURIComponent(row.id)}`
}

/** What the board's query string asks to have open: a planner item or a Torque task. */
export function parseBoardOpen(search: URLSearchParams): { item?: { db: PlannerDb; id: string }; task?: string } {
  const open = search.get('open') ?? ''
  const slash = open.indexOf('/')
  const db = slash > 0 ? open.slice(0, slash) : ''
  const id = slash > 0 ? decodeURIComponent(open.slice(slash + 1)) : ''
  const task = search.get('task') ?? ''
  return {
    item: isPlannerDb(db) && id ? { db, id } : undefined,
    task: task || undefined,
  }
}

/** "5m", "3h", "2d": how long ago, rounded the way the text board does. */
export function boardAge(iso: string, nowMs: number): string {
  const t = Date.parse(iso)
  if (!Number.isFinite(t)) return ''
  const m = Math.max(0, Math.round((nowMs - t) / 60000))
  if (m < 60) return `${m}m`
  if (m < 2880) return `${Math.round(m / 60)}h`
  return `${Math.round(m / 1440)}d`
}

/** "(+4 more)" when a section holds more rows than it shows. */
export function boardMore(shown: number, total: number): string {
  return total > shown ? `+${total - shown} more` : ''
}
