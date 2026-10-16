// Pure helpers for the diff viewer: row building (unified/split), syntax-light
// tokenizing, comment-thread anchoring (incl. outdated detection), file tree.
import type { DiffLine, FileDiff, ForgeComment, ReviewComment, CommentSide } from '../types'

// ---------- syntax-light tokenizer ----------

export type TokenKind = 'plain' | 'keyword' | 'string' | 'comment' | 'number'
export interface Token {
  text: string
  kind: TokenKind
}

type Lang = 'ts' | 'go' | 'py' | 'sh' | 'json' | 'css' | 'md' | 'plain'

const EXT: Record<string, Lang> = {
  ts: 'ts', tsx: 'ts', js: 'ts', jsx: 'ts', mjs: 'ts', cjs: 'ts', java: 'ts', rs: 'ts', c: 'ts', h: 'ts', cpp: 'ts',
  go: 'go', py: 'py', rb: 'py', sh: 'sh', bash: 'sh', yml: 'sh', yaml: 'sh', toml: 'sh', json: 'json',
  css: 'css', scss: 'css', md: 'md', mdx: 'md',
}

export function langOf(path: string): Lang {
  const base = path.split('/').pop() ?? ''
  if (base === 'Dockerfile' || base === 'Makefile') return 'sh'
  const ext = base.includes('.') ? base.split('.').pop()!.toLowerCase() : ''
  return EXT[ext] ?? 'plain'
}

const KEYWORDS: Record<Lang, Set<string>> = {
  ts: new Set('import export from default const let var function return if else for while switch case break continue new class extends implements interface type enum async await try catch finally throw typeof instanceof in of null undefined true false this void static public private readonly as fn pub use mod struct impl match mut'.split(' ')),
  go: new Set('package import func return if else for range switch case break continue type struct interface map chan go defer select var const nil true false'.split(' ')),
  py: new Set('def class return if elif else for while import from as with try except finally raise pass lambda None True False in is not and or yield async await self'.split(' ')),
  sh: new Set('if then else fi for do done while case esac function export echo local true false'.split(' ')),
  json: new Set(['true', 'false', 'null']),
  css: new Set(),
  md: new Set(),
  plain: new Set(),
}

const LINE_COMMENT: Partial<Record<Lang, string>> = { ts: '//', go: '//', py: '#', sh: '#', css: '//' }

/** Single-line tokenizer: strings, line comments, numbers, keywords. No cross-line state by design. */
export function tokenize(text: string, lang: Lang): Token[] {
  if (lang === 'plain' || lang === 'md' || text.length > 600) return [{ text, kind: 'plain' }]
  const out: Token[] = []
  const kw = KEYWORDS[lang]
  const lc = LINE_COMMENT[lang]
  let buf = ''
  const flush = () => {
    if (buf) out.push({ text: buf, kind: 'plain' })
    buf = ''
  }
  let i = 0
  while (i < text.length) {
    const c = text[i]
    if (lc && text.startsWith(lc, i)) {
      flush()
      out.push({ text: text.slice(i), kind: 'comment' })
      return out
    }
    if (c === '"' || c === "'" || c === '`') {
      let j = i + 1
      while (j < text.length && text[j] !== c) j += text[j] === '\\' ? 2 : 1
      flush()
      out.push({ text: text.slice(i, Math.min(j + 1, text.length)), kind: 'string' })
      i = j + 1
      continue
    }
    if (/[0-9]/.test(c) && !/[A-Za-z_$]/.test(text[i - 1] ?? '')) {
      let j = i + 1
      while (j < text.length && /[0-9a-fx._]/i.test(text[j])) j++
      flush()
      out.push({ text: text.slice(i, j), kind: 'number' })
      i = j
      continue
    }
    if (/[A-Za-z_$]/.test(c)) {
      let j = i + 1
      while (j < text.length && /[A-Za-z0-9_$]/.test(text[j])) j++
      const word = text.slice(i, j)
      if (kw.has(word)) {
        flush()
        out.push({ text: word, kind: 'keyword' })
      } else buf += word
      i = j
      continue
    }
    buf += c
    i++
  }
  flush()
  return out
}

// ---------- rows ----------

export interface HunkRow {
  type: 'hunk'
  key: string
  header: string
}
export interface UnifiedLineRow {
  type: 'line'
  key: string
  line: DiffLine
}
export interface SplitRow {
  type: 'split'
  key: string
  left?: DiffLine
  right?: DiffLine
}
export type UnifiedRow = HunkRow | UnifiedLineRow
export type SplitDiffRow = HunkRow | SplitRow

const hunkLabel = (h: FileDiff['hunks'][number]): string =>
  `@@ -${h.oldStart},${h.oldLines} +${h.newStart},${h.newLines} @@${h.header ? ` ${h.header}` : ''}`

export function unifiedRows(file: FileDiff): UnifiedRow[] {
  const rows: UnifiedRow[] = []
  file.hunks.forEach((h, hi) => {
    rows.push({ type: 'hunk', key: `h${hi}`, header: hunkLabel(h) })
    h.lines.forEach((line, li) => rows.push({ type: 'line', key: `h${hi}l${li}`, line }))
  })
  return rows
}

/** Pair runs of deletions with the additions that follow so they sit side by side. */
export function splitRows(file: FileDiff): SplitDiffRow[] {
  const rows: SplitDiffRow[] = []
  file.hunks.forEach((h, hi) => {
    rows.push({ type: 'hunk', key: `h${hi}`, header: hunkLabel(h) })
    let i = 0
    while (i < h.lines.length) {
      const l = h.lines[i]
      if (l.kind === 'context') {
        rows.push({ type: 'split', key: `h${hi}l${i}`, left: l, right: l })
        i++
        continue
      }
      const dels: DiffLine[] = []
      const adds: DiffLine[] = []
      const start = i
      while (i < h.lines.length && h.lines[i].kind === 'del') dels.push(h.lines[i++])
      while (i < h.lines.length && h.lines[i].kind === 'add') adds.push(h.lines[i++])
      const n = Math.max(dels.length, adds.length)
      for (let k = 0; k < n; k++) rows.push({ type: 'split', key: `h${hi}l${start}-${k}`, left: dels[k], right: adds[k] })
    }
  })
  return rows
}

export function diffLineCount(file: FileDiff): number {
  return file.hunks.reduce((n, h) => n + h.lines.length, 0)
}

/** Files at or above this many lines start collapsed and load on demand. */
export const LARGE_FILE_LINES = 400
/** Rendered rows per "show more" window inside a large file. */
export const ROW_CHUNK = 300

// ---------- anchors and threads ----------

export const anchorKey = (path: string, side: CommentSide, line: number): string => `${path}:${side}:${line}`

/** Anchor of a rendered diff line: deletions are left/oldLine, everything else right/newLine. */
export function lineAnchor(path: string, line: DiffLine): string | null {
  if (line.kind === 'del') return line.oldLine != null ? anchorKey(path, 'left', line.oldLine) : null
  return line.newLine != null ? anchorKey(path, 'right', line.newLine) : null
}

export interface Thread {
  id: string
  root: ReviewComment
  replies: ReviewComment[]
  path: string
  /** Anchored to an older commit than the PR head. */
  outdated: boolean
  resolved: boolean
}

export interface ThreadIndex {
  /** Threads whose anchor line is visible in the diff, by anchorKey. */
  byAnchor: Map<string, Thread[]>
  /** Threads on a file in the diff but not on a visible line (or file-level). Keyed by path. */
  detached: Map<string, Thread[]>
  /** Threads whose file is no longer in the diff. */
  orphaned: Thread[]
  issue: ForgeComment[]
  all: Thread[]
}

export function buildThreadIndex(comments: readonly ForgeComment[], files: readonly FileDiff[], headSha?: string): ThreadIndex {
  const visible = new Set<string>()
  for (const f of files) for (const h of f.hunks) for (const l of h.lines) {
    const k = lineAnchor(f.path, l)
    if (k) visible.add(k)
  }
  const paths = new Set(files.map((f) => f.path))
  const reviews = comments.filter((c): c is ReviewComment => c.kind === 'review')
  const issue = comments.filter((c) => c.kind === 'issue')
  const byId = new Map(reviews.map((c) => [c.id, c]))
  const rootOf = (c: ReviewComment): ReviewComment => {
    let cur = c
    for (let guard = 0; cur.inReplyTo && byId.has(cur.inReplyTo) && guard < 50; guard++) cur = byId.get(cur.inReplyTo)!
    return cur
  }
  const threads = new Map<string, Thread>()
  for (const c of reviews) {
    const root = rootOf(c)
    let t = threads.get(root.id)
    if (!t) {
      const outdated = !!(root.commitSha && headSha && root.commitSha !== headSha)
      t = { id: root.id, root, replies: [], path: root.path, outdated, resolved: !!root.resolved }
      threads.set(root.id, t)
    }
    if (c.id !== root.id) t.replies.push(c)
  }
  const index: ThreadIndex = { byAnchor: new Map(), detached: new Map(), orphaned: [], issue, all: [...threads.values()] }
  for (const t of index.all) {
    t.replies.sort((a, b) => a.createdAt.localeCompare(b.createdAt))
    if (!paths.has(t.path)) {
      index.orphaned.push(t)
      continue
    }
    const key = t.root.line != null ? anchorKey(t.path, t.root.side ?? 'right', t.root.line) : null
    // An outdated thread's line numbers refer to another commit, so trust the
    // anchor only when that line is still present in the current diff.
    if (key && visible.has(key) && !t.outdated) {
      index.byAnchor.set(key, [...(index.byAnchor.get(key) ?? []), t])
    } else {
      index.detached.set(t.path, [...(index.detached.get(t.path) ?? []), t])
    }
  }
  return index
}

// ---------- file tree ----------

export interface TreeNode {
  name: string
  path: string
  dir: boolean
  children: TreeNode[]
  file?: FileDiff
}

export function buildFileTree(files: readonly FileDiff[]): TreeNode[] {
  const root: TreeNode = { name: '', path: '', dir: true, children: [] }
  for (const f of files) {
    const parts = f.path.split('/')
    let cur = root
    parts.forEach((part, i) => {
      const path = parts.slice(0, i + 1).join('/')
      const last = i === parts.length - 1
      let next = cur.children.find((c) => c.name === part && c.dir === !last)
      if (!next) {
        next = { name: part, path, dir: !last, children: [], ...(last ? { file: f } : {}) }
        cur.children.push(next)
      }
      cur = next
    })
  }
  const compact = (n: TreeNode): TreeNode => {
    n.children = n.children.map(compact)
    // Fold single-child directory chains: a/b/c.
    while (n.dir && n.name && n.children.length === 1 && n.children[0].dir) {
      const c = n.children[0]
      n = { ...c, name: `${n.name}/${c.name}` }
    }
    n.children.sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1))
    return n
  }
  return root.children.map(compact).sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1))
}

export function statusGlyph(f: FileDiff): string {
  switch (f.status) {
    case 'added': return 'A'
    case 'removed': return 'D'
    case 'renamed': return 'R'
    default: return 'M'
  }
}

export const fileAnchorId = (path: string): string => `file-${path.replace(/[^A-Za-z0-9]+/g, '-')}`
