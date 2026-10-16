import { createElement } from 'react'
import { refIcon } from './extensions'

/** The glyph for an icon token (`RefKind.icon`), via the extensible icon registry. */
export function RefIcon({ token, className }: { token: string; className?: string }) {
  return createElement(refIcon(token), { className, 'aria-hidden': true })
}
