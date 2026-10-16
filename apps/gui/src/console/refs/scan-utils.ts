// Shared scanning helpers. Everything here is linear in the input.

const TRAILING_DROP = new Set(['.', ',', ';', ':', '!', '?', "'", '"', '*'])
const PAIRS: Record<string, string> = { ')': '(', ']': '[', '}': '{', '>': '<' }

/**
 * Where a match ending at `end` really ends once sentence punctuation and
 * unbalanced closers are dropped: "see src/a.ts." and "(see https://x.io/a)"
 * stop before the "." and the ")", while "https://x.io/a_(b)" keeps its pair.
 */
export function trimTrailing(text: string, start: number, end: number): number {
  let e = end
  while (e > start) {
    const c = text[e - 1]
    if (TRAILING_DROP.has(c)) {
      e--
      continue
    }
    const open = PAIRS[c]
    if (open) {
      let opens = 0
      let closes = 0
      for (let i = start; i < e; i++) {
        if (text[i] === open) opens++
        else if (text[i] === c) closes++
      }
      if (closes > opens) {
        e--
        continue
      }
    }
    break
  }
  return e
}

const WORD = /[A-Za-z0-9_]/

export function isWordChar(c: string | undefined): boolean {
  return c !== undefined && WORD.test(c)
}

/** Run a global regex over text, yielding exec results. Guards against zero-width loops. */
export function* matches(re: RegExp, text: string): Generator<RegExpExecArray> {
  const g = new RegExp(re.source, re.flags.includes('g') ? re.flags : re.flags + 'g')
  let m: RegExpExecArray | null
  while ((m = g.exec(text)) !== null) {
    if (m[0].length === 0) {
      g.lastIndex++
      continue
    }
    yield m
  }
}
