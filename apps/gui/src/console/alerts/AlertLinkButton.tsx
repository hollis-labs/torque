import { Link } from 'react-router-dom'
import { isInternalHref, safeHref } from './policy'
import type { AlertLink } from './types'

const CLS =
  'inline-flex items-center border border-zinc-700 px-2 py-0.5 font-mono text-[10px] uppercase tracking-widest text-zinc-200 hover:border-zinc-500 hover:bg-zinc-900 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300'

/** The alert's primary action in phase 1: a validated link. */
export function AlertLinkButton({ link, onFollow }: { link: AlertLink; onFollow?: () => void }) {
  const href = safeHref(link.href)
  if (!href) return null
  if (isInternalHref(href))
    return (
      <Link to={href} className={CLS} onClick={onFollow}>
        {link.label}
      </Link>
    )
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className={CLS} onClick={onFollow}>
      {link.label}
    </a>
  )
}
