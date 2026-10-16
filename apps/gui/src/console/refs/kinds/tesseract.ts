import type { RefHit, RefKind } from '../types'
import { isWordChar, matches, trimTrailing } from '../scan-utils'

// Item ids are ULIDs: 26 Crockford base32 characters, first one 0-7.
const ULID = /[0-7][0-9A-HJKMNP-TV-Z]{25}/g

// `tesseract:[domain@]namespace#key` is explicit. Bare `namespace#key` is
// accepted when the namespace starts with a known root, which keeps
// `issue#12` and `C#` out.
const KEY_BODY = '([a-z][a-z0-9_.-]{0,63}(?:\\/[A-Za-z0-9_.-]{1,64}){0,10})#([A-Za-z0-9_.:-]{1,160})'
const EXPLICIT = new RegExp(`tesseract:(?:([a-z]{3,16})@)?${KEY_BODY}`, 'g')
const BARE = new RegExp(`(?:project|user|agent|shared|global|system)\\/[A-Za-z0-9_.-]{1,64}(?:\\/[A-Za-z0-9_.-]{1,64}){0,9}#[A-Za-z0-9_.:-]{1,160}`, 'g')
const EXPLICIT_ULID = /tesseract:([0-7][0-9A-HJKMNP-TV-Z]{25})/g

function isUlid(s: string): boolean {
  return /\d/.test(s) && /[A-Z]/.test(s)
}

export const TESSERACT_ITEM_KIND = 'tesseract.item'
export const TESSERACT_KEY_KIND = 'tesseract.key'

/** `[domain@]namespace#key`: the canonical form for a memory key. */
export function tesseractKeyCanonical(namespace: string, key: string, domain?: string): string {
  return `${domain ? `${domain}@` : ''}${namespace}#${key}`
}

export function parseTesseractKey(canonical: string): { domain?: string; namespace: string; key: string } | null {
  const m = /^(?:([a-z]{3,16})@)?([^#@]+)#(.+)$/.exec(canonical)
  return m ? { domain: m[1], namespace: m[2], key: m[3] } : null
}

function keyHit(text: string, start: number, end: number): RefHit | null {
  const before = text[start - 1]
  if (isWordChar(before) || before === '/' || before === '.' || before === '-') return null
  const e = trimTrailing(text, start, end)
  const raw = text.slice(start, e).replace(/^tesseract:/, '')
  if (!parseTesseractKey(raw)) return null
  return { start, end: e, canonical: raw }
}

export function createTesseractKinds(): RefKind[] {
  const item: RefKind = {
    id: TESSERACT_ITEM_KIND,
    label: 'Tesseract item',
    icon: 'memory',
    priority: 70,
    scan(text) {
      const hits: RefHit[] = []
      for (const m of matches(EXPLICIT_ULID, text)) {
        const before = text[m.index - 1]
        if (isWordChar(before)) continue
        if (isWordChar(text[m.index + m[0].length])) continue
        hits.push({ start: m.index, end: m.index + m[0].length, canonical: m[1] })
      }
      for (const m of matches(ULID, text)) {
        const start = m.index
        const end = start + m[0].length
        const before = text[start - 1]
        const after = text[end]
        if (isWordChar(before) || before === '-' || before === '/' || before === ':') continue
        if (isWordChar(after) || after === '-' || after === '/') continue
        if (!isUlid(m[0])) continue
        hits.push({ start, end, canonical: m[0] })
      }
      return hits
    },
    chipLabel: (ref) => `${ref.canonical.slice(0, 4)}…${ref.canonical.slice(-4)}`,
    open: () => ({ type: 'none' }),
  }

  const key: RefKind = {
    id: TESSERACT_KEY_KIND,
    label: 'Tesseract key',
    icon: 'memory',
    priority: 90,
    scan(text) {
      const hits: RefHit[] = []
      for (const m of matches(EXPLICIT, text)) {
        const h = keyHit(text, m.index, m.index + m[0].length)
        if (h) hits.push(h)
      }
      for (const m of matches(BARE, text)) {
        const h = keyHit(text, m.index, m.index + m[0].length)
        if (h) hits.push(h)
      }
      return hits
    },
    chipLabel: (ref) => parseTesseractKey(ref.canonical)?.key ?? ref.canonical,
    open: () => ({ type: 'none' }),
  }

  return [item, key]
}
