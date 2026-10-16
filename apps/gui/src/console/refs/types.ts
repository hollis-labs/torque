// Reference registry contracts (Agent Console F2).
//
// Pure TypeScript, no React and no network: the detector turns text into
// segments, the registry holds the kinds, the resolver client fetches the
// entities behind references. R1 renders; G2 adds forge kinds (PR, commit,
// repo) by calling `registry.register(...)` with a RefKind.

/** Dotted lowercase id, e.g. `torque.task`, `tesseract.key`, `forge.pr`. */
export type RefKindId = string

export type RefMeta = Readonly<Record<string, string | number>>

/** A candidate match a kind found in the text, as offsets into that exact string. */
export interface RefHit {
  start: number
  /** Exclusive. */
  end: number
  /** Stable identity of the referenced thing: what resolvers and caches are keyed by. */
  canonical: string
  meta?: RefMeta
}

/** Where activating a reference goes. `copy` is for things with no destination (a file path). */
export type OpenTarget =
  | { type: 'route'; to: string }
  | { type: 'external'; href: string }
  | { type: 'copy'; text: string }
  | { type: 'none' }

export interface RefIdentity {
  kind: RefKindId
  canonical: string
  meta?: RefMeta
}

export interface RefKind {
  id: RefKindId
  /** Human name for legends and settings ("Torque task"). */
  label: string
  /** Icon token the renderer maps to a glyph. A string, so this module stays UI-free. */
  icon: string
  /**
   * Overlap tie-break: the higher priority wins a contested span; equal
   * priority falls back to the longer match, then the earlier one. Built-ins
   * use 40 (path) to 100 (url); see builtin.ts.
   */
  priority: number
  /**
   * Find candidates. Must be pure and linear in the text (no nested
   * quantifiers), may over-report (the detector resolves overlaps and code
   * spans), and must not throw on any input.
   */
  scan(text: string): RefHit[]
  /** Short text for the inline chip. Defaults to the matched text. */
  chipLabel?(ref: RefIdentity, raw: string): string
  open(ref: RefIdentity): OpenTarget
}

export type Segment =
  | { type: 'text'; text: string; start: number; end: number }
  | {
      type: 'ref'
      kind: RefKindId
      canonical: string
      /** The matched source text, exactly as written. */
      raw: string
      start: number
      end: number
      meta?: RefMeta
    }

export type RefSegment = Extract<Segment, { type: 'ref' }>

// ---------- resolution ----------

export type StatusTone = 'neutral' | 'info' | 'success' | 'warning' | 'danger'

/** What a resolver knows about a reference: the data a hovercard or chip needs, in a UI-neutral shape. */
export interface RefEntity {
  title: string
  subtitle?: string
  description?: string
  status?: { label: string; tone: StatusTone }
  fields?: { label: string; value: string; mono?: boolean }[]
  /** ISO timestamp of the thing's last change, for "updated 3m ago". */
  updatedAt?: string
}

export type ResolveOutcome = { status: 'found'; entity: RefEntity } | { status: 'not-found' }

export interface ResolveContext {
  signal: AbortSignal
}

/**
 * Backend seam for one kind. One call resolves a batch; a canonical missing
 * from the result counts as not-found; a thrown error marks the whole batch
 * as failed (retryable).
 */
export interface KindResolver {
  resolveMany(canonicals: string[], ctx: ResolveContext): Promise<Record<string, ResolveOutcome | undefined>>
}

export type RefState =
  | { status: 'idle' }
  | { status: 'loading'; stale?: RefEntity }
  | { status: 'found'; entity: RefEntity; fetchedAt: number }
  | { status: 'not-found'; fetchedAt: number }
  | { status: 'error'; message: string; fetchedAt: number; stale?: RefEntity }
  /** No resolver registered for this kind: detection and linking only. */
  | { status: 'unsupported' }

export type RefCardPhase = 'loading' | 'ready' | 'not-found' | 'error' | 'unresolved'

/** Everything a hovercard renders, derived from a RefState. */
export interface PreviewCard {
  kind: RefKindId
  kindLabel: string
  icon: string
  canonical: string
  phase: RefCardPhase
  title: string
  subtitle?: string
  description?: string
  status?: RefEntity['status']
  fields: NonNullable<RefEntity['fields']>
  updatedAt?: string
  error?: string
  open: OpenTarget
}
