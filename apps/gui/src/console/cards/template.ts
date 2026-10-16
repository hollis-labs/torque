import type { ConsoleEvent } from '../events/types'

/**
 * Tiny template language for rule strings. `{a|b|c}` takes the first
 * non-empty of its alternatives. Paths: `id`, `kind`, `threadId`, `correlationId`,
 * `payload.<key>`, `subject.<type>` (id of first subject of that type),
 * `subject.<type>.label` (its label, else id). Missing values render as "".
 * Values are plain text only; nothing is evaluated.
 */
export function resolvePath(e: ConsoleEvent, path: string): string {
  const p = path.trim()
  if (p === 'id' || p === 'kind' || p === 'threadId' || p === 'correlationId') {
    const v = e[p]
    return v === undefined ? '' : String(v)
  }
  if (p.startsWith('payload.')) {
    const v = (e.payload as Record<string, unknown> | undefined)?.[p.slice(8)]
    return v === undefined || v === null || typeof v === 'object' ? '' : String(v)
  }
  if (p.startsWith('subject.')) {
    const [, type, field] = p.split('.')
    const s = e.subjects.find((x) => x.type === type)
    if (!s) return ''
    return field === 'label' ? (s.label ?? s.id) : s.id
  }
  return ''
}

export function renderTemplate(tpl: string, e: ConsoleEvent): string {
  return tpl.replace(/\{([^}]*)\}/g, (_, expr: string) => {
    for (const alt of expr.split('|')) {
      const v = resolvePath(e, alt)
      if (v) return v
    }
    return ''
  })
}
