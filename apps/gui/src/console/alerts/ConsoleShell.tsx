import { useState, type ReactNode } from 'react'
import { ConsoleProvider } from '../events/provider'
import { resolveConsoleMode } from '../events/mode'
import { ConsoleEnabledContext } from '../shell/enabled'
import { ConsoleIntegration } from '../shell/ConsoleIntegration'
import { isConsoleEnabled } from './enabled'

/**
 * The single shell mount point for the Console. Place around the app shell,
 * inside ApiProvider and the router. Disabled (renders children untouched)
 * unless `?console=fixture|live` or VITE_CONSOLE_SOURCE is set. When on it also mounts
 * messaging, chat dock, card kinds (shell/ConsoleIntegration). The decision is
 * made once at mount, so in-app navigation that drops the query keeps it on.
 */
export function ConsoleShell({ children }: { children: ReactNode }) {
  const [{ enabled, mode }] = useState(() => {
    const input = { search: typeof window !== 'undefined' ? window.location.search : '', env: import.meta.env.VITE_CONSOLE_SOURCE as string | undefined }
    return { enabled: isConsoleEnabled(input), mode: resolveConsoleMode(input) }
  })
  if (!enabled) return <>{children}</>
  return (
    <ConsoleEnabledContext.Provider value>
      <ConsoleProvider>
        <ConsoleIntegration mode={mode}>{children}</ConsoleIntegration>
      </ConsoleProvider>
    </ConsoleEnabledContext.Provider>
  )
}
