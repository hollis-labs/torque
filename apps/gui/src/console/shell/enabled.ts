import { createContext, useContext } from 'react'

/** True below an enabled <ConsoleShell> (`?console=fixture|live` / VITE_CONSOLE_SOURCE). False otherwise. */
export const ConsoleEnabledContext = createContext(false)

export function useConsoleEnabled(): boolean {
  return useContext(ConsoleEnabledContext)
}
