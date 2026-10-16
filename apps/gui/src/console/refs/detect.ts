import type { RefRegistry } from './registry'
import type { RefHit, RefKind, RefSegment, Segment } from './types'

export interface DetectOptions {
  /** Leave fenced blocks and `inline code` alone. Default true. */
  skipCode?: boolean
  /** Only run these kinds. Default: all registered. */
  kinds?: readonly string[]
  /** Called when a kind throws or returns a malformed hit; the kind is skipped, detection continues. */
  onError?: (kind: string, err: unknown) => void
}

export type Range = readonly [start: number, end: number]

// ---------- code ranges ----------

/** Fenced code blocks (``` or ~~~, closed by a run at least as long; unterminated runs to the end). */
function fencedRanges(text: string): Range[] {
  const out: Range[] = []
  let pos = 0
  let open: { start: number; ch: string; len: number } | null = null
  while (pos <= text.length) {
    let eol = text.indexOf('\n', pos)
    if (eol === -1) eol = text.length
    const line = text.slice(pos, eol)
    const m = /^ {0,3}(`{3,}|~{3,})/.exec(line)
    if (m) {
      const ch = m[1][0]
      const len = m[1].length
      if (!open) {
        // A backtick fence's info string may not contain backticks.
        if (!(ch === '`' && line.slice(m[0].length).includes('`'))) open = { start: pos, ch, len }
      } else if (ch === open.ch && len >= open.len && line.slice(m[0].length).trim() === '') {
        out.push([open.start, Math.min(eol + 1, text.length)])
        open = null
      }
    }
    pos = eol + 1
  }
  if (open) out.push([open.start, text.length])
  return out
}

/** Backtick spans outside the given ranges: a run closes at the next run of the same length. */
function inlineCodeRanges(text: string, skip: Range[]): Range[] {
  const runs: { start: number; len: number }[] = []
  let k = 0
  for (let i = 0; i < text.length; i++) {
    if (text[i] !== '`') continue
    while (k < skip.length && skip[k][1] <= i) k++
    if (k < skip.length && skip[k][0] <= i) {
      i = skip[k][1] - 1
      continue
    }
    let j = i
    while (j < text.length && text[j] === '`') j++
    runs.push({ start: i, len: j - i })
    i = j - 1
  }
  // nextSame[i]: index of the next run with the same length, built right to left.
  const nextSame: number[] = new Array(runs.length).fill(-1)
  const lastByLen = new Map<number, number>()
  for (let i = runs.length - 1; i >= 0; i--) {
    nextSame[i] = lastByLen.get(runs[i].len) ?? -1
    lastByLen.set(runs[i].len, i)
  }
  const out: Range[] = []
  for (let i = 0; i < runs.length; i++) {
    const j = nextSame[i]
    if (j === -1) continue
    out.push([runs[i].start, runs[j].start + runs[j].len])
    // Runs between opener and closer are code content.
    while (i + 1 < runs.length && runs[i + 1].start < runs[j].start + runs[j].len) i++
  }
  return out
}

/** Ranges of code in the text, sorted and disjoint. */
export function codeRanges(text: string): Range[] {
  const fences = fencedRanges(text)
  return [...fences, ...inlineCodeRanges(text, fences)].sort((a, b) => a[0] - b[0])
}

function intersects(ranges: Range[], start: number, end: number): boolean {
  // Binary search for the first range ending after `start`.
  let lo = 0
  let hi = ranges.length
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (ranges[mid][1] <= start) lo = mid + 1
    else hi = mid
  }
  return lo < ranges.length && ranges[lo][0] < end
}

// ---------- detection ----------

interface Candidate extends RefHit {
  kind: RefKind
}

function valid(h: RefHit, len: number): boolean {
  return (
    Number.isInteger(h.start) && Number.isInteger(h.end) && h.start >= 0 && h.end <= len && h.start < h.end &&
    typeof h.canonical === 'string' && h.canonical.length > 0
  )
}

/** Higher priority wins; then the longer span; then the earlier start; then registration order. */
function better(a: Candidate, b: Candidate): number {
  return b.kind.priority - a.kind.priority || b.end - b.start - (a.end - a.start) || a.start - b.start
}

/** All non-overlapping reference matches, in text order. */
export function detectRefs(text: string, registry: RefRegistry, opts: DetectOptions = {}): RefSegment[] {
  if (!text) return []
  const skipCode = opts.skipCode ?? true
  const code = skipCode ? codeRanges(text) : []
  const wanted = opts.kinds ? new Set(opts.kinds) : null

  const cands: Candidate[] = []
  for (const kind of registry.list()) {
    if (wanted && !wanted.has(kind.id)) continue
    let hits: RefHit[]
    try {
      hits = kind.scan(text)
    } catch (err) {
      opts.onError?.(kind.id, err)
      continue
    }
    for (const h of hits) {
      if (!valid(h, text.length)) {
        opts.onError?.(kind.id, new Error(`invalid hit ${JSON.stringify([h.start, h.end])}`))
        continue
      }
      if (code.length && intersects(code, h.start, h.end)) continue
      cands.push({ ...h, kind })
    }
  }

  // Greedy by rank; stable sort keeps registration order on full ties.
  const ranked = [...cands].sort(better)
  const taken: Candidate[] = []
  for (const c of ranked) {
    // `taken` is kept sorted by start; check the neighbours only.
    let lo = 0
    let hi = taken.length
    while (lo < hi) {
      const mid = (lo + hi) >> 1
      if (taken[mid].start < c.start) lo = mid + 1
      else hi = mid
    }
    const prev = taken[lo - 1]
    const next = taken[lo]
    if ((prev && prev.end > c.start) || (next && next.start < c.end)) continue
    taken.splice(lo, 0, c)
  }

  return taken.map((c) => ({
    type: 'ref' as const,
    kind: c.kind.id,
    canonical: c.canonical,
    raw: text.slice(c.start, c.end),
    start: c.start,
    end: c.end,
    ...(c.meta ? { meta: c.meta } : {}),
  }))
}

/**
 * Split text into alternating text and ref segments that concatenate back to
 * the input exactly (`segments.map(s => text.slice(s.start, s.end)).join('') === text`).
 */
export function detect(text: string, registry: RefRegistry, opts: DetectOptions = {}): Segment[] {
  const refs = detectRefs(text, registry, opts)
  const out: Segment[] = []
  let at = 0
  for (const r of refs) {
    if (r.start > at) out.push({ type: 'text', text: text.slice(at, r.start), start: at, end: r.start })
    out.push(r)
    at = r.end
  }
  if (at < text.length) out.push({ type: 'text', text: text.slice(at), start: at, end: text.length })
  return out
}

/** Distinct references in a set of segments, for prefetching: first occurrence order. */
export function uniqueRefs(segments: readonly Segment[]): { kind: string; canonical: string }[] {
  const seen = new Set<string>()
  const out: { kind: string; canonical: string }[] = []
  for (const s of segments) {
    if (s.type !== 'ref') continue
    const key = refKey(s.kind, s.canonical)
    if (seen.has(key)) continue
    seen.add(key)
    out.push({ kind: s.kind, canonical: s.canonical })
  }
  return out
}

export function refKey(kind: string, canonical: string): string {
  return `${kind}\u0000${canonical}`
}
