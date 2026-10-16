import { Children, Fragment, isValidElement, type ComponentProps, type ElementType, type ReactNode } from 'react'
import type { Components } from 'react-markdown'
import { LinkText } from './LinkText'
import type { AutolinkSurface } from './surfaces'

function linkChildren(surface: AutolinkSurface, children: ReactNode): ReactNode {
  return Children.map(children, (c, i) =>
    typeof c === 'string' ? <LinkText key={i} surface={surface} text={c} /> : isValidElement(c) ? c : <Fragment key={i}>{c}</Fragment>,
  )
}

type El<K extends keyof React.JSX.IntrinsicElements> = ComponentProps<K> & { node?: unknown }


/**
 * react-markdown `components` that linkify plain text in paragraphs, list items,
 * headings, emphasis and table cells. Code spans/fences and existing links are
 * left alone (their elements are not overridden).
 */
export function linkifiedMarkdownComponents(surface: AutolinkSurface): Components {
  const wrap = <K extends keyof React.JSX.IntrinsicElements>(Tag: K) => {
    const C = ({ node, children, ...props }: El<K>) => {
      void node
      const T = Tag as unknown as ElementType
      return <T {...props}>{linkChildren(surface, children as ReactNode)}</T>
    }
    return C
  }
  return {
    p: wrap('p'), li: wrap('li'), h1: wrap('h1'), h2: wrap('h2'), h3: wrap('h3'), h4: wrap('h4'),
    strong: wrap('strong'), em: wrap('em'), td: wrap('td'), th: wrap('th'), blockquote: wrap('blockquote'),
  } as Components
}
