import { Fragment, useMemo } from 'react'
import { detect, type DetectOptions } from '../refs'
import { useAutolink } from './context'
import { RefChip, type RefChipProps } from './RefChip'

export interface LinkifiedProps {
  text: string
  /** Only link these kinds (e.g. `['torque.task']` in a dense table). Default: all registered. */
  kinds?: DetectOptions['kinds']
  /** Link inside `code` spans and fences too. Default false. */
  includeCode?: boolean
  /** Preserve newlines and runs of spaces. */
  preserveWhitespace?: boolean
  className?: string
  chip?: Pick<RefChipProps, 'resolve' | 'hoverDelay' | 'hovercard' | 'className'>
}

/**
 * Renders text with references (tasks, epics, Tesseract keys, agents, paths,
 * URLs, and any kind a feature registers) as chips. Output is a `<span>` so it
 * drops into any inline context. Every chip requests its entity on mount and
 * the resolver client coalesces the whole page into one call per kind, so
 * hundreds of chips cost a handful of requests.
 */
export function Linkified({ text, kinds, includeCode = false, preserveWhitespace = false, className, chip }: LinkifiedProps) {
  const { registry } = useAutolink()
  const version = registry.version
  const kindsKey = kinds?.join(',')
  const segments = useMemo(
    () => detect(text, registry, { skipCode: !includeCode, ...(kinds ? { kinds } : {}) }),
    // `version` invalidates when a feature registers a kind after first render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [text, registry, version, kindsKey, includeCode],
  )
  return (
    <span className={`${preserveWhitespace ? 'whitespace-pre-wrap' : ''} ${className ?? ''}`.trim() || undefined}>
      {segments.map((s) =>
        s.type === 'text' ? (
          <Fragment key={s.start}>{s.text}</Fragment>
        ) : (
          <RefChip key={s.start} kind={s.kind} canonical={s.canonical} raw={s.raw} meta={s.meta} {...chip} />
        ),
      )}
    </span>
  )
}
