import { useMemo, type ReactNode } from 'react'
import { useApi } from '@/hooks/use-api'
import { detect } from '../refs'
import { useAutolink } from './context'
import { createLiveResolvers } from './live'
import { AutolinkProvider } from './Provider'
import { Linkified, type LinkifiedProps } from './Linkified'
import { isAutolinkSurfaceEnabled, type AutolinkSurface } from './surfaces'

export interface LinkTextProps extends Omit<LinkifiedProps, 'text'> {
  surface: AutolinkSurface
  text: string
}

/** Linkified for a named surface: plain text when that surface is opted out. */
export function LinkText({ surface, text, ...rest }: LinkTextProps) {
  const { registry } = useAutolink()
  const enabled = isAutolinkSurfaceEnabled(surface)
  const kindsKey = rest.kinds?.join(',')
  const { includeCode, kinds } = rest
  // Text with no references stays a bare text node (no wrapper span, no layout change).
  const hasRef = useMemo(
    () => enabled && detect(text, registry, { skipCode: !includeCode, ...(kinds ? { kinds } : {}) }).some((x) => x.type !== 'text'),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [enabled, text, registry, registry.version, includeCode, kindsKey],
  )
  if (!hasRef) return <>{text}</>
  return <Linkified text={text} {...rest} />
}

/** Mount once at the shell (inside ApiProvider and the router, above ConsoleShell). */
export function ShellAutolink({ children }: { children: ReactNode }) {
  const api = useApi()
  const resolvers = useMemo(() => createLiveResolvers(api), [api])
  return <AutolinkProvider resolvers={resolvers}>{children}</AutolinkProvider>
}

