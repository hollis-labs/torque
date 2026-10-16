import { useState, type ReactNode } from 'react'
import { ConsoleProvider } from '../events/provider'
import { AlertHost } from './AlertHost'
import { DEFAULT_RULE_PACK } from '../rules/default-pack'
import { isConsoleEnabled } from './enabled'

/**
 * The single shell mount point for the Console. Place around the app shell,
 * inside ApiProvider and the router. Disabled (renders children untouched)
 * unless `?console=fixture|live` or VITE_CONSOLE_SOURCE is set. The decision is
 * made once at mount, so in-app navigation that drops the query keeps it on.
 */
export function ConsoleShell({ children }: { children: ReactNode }) {
  const [enabled] = useState(() =>
    isConsoleEnabled({ search: typeof window !== 'undefined' ? window.location.search : '', env: import.meta.env.VITE_CONSOLE_SOURCE as string | undefined }),
  )
  if (!enabled) return <>{children}</>
  return (
    <ConsoleProvider>
      <AlertHost rules={DEFAULT_RULE_PACK}>{children}</AlertHost>
    </ConsoleProvider>
  )
}
