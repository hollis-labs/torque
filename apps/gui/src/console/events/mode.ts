export type ConsoleMode = 'fixture' | 'live'

/**
 * Dev switch: `?console=fixture|live` in the URL wins, then VITE_CONSOLE_SOURCE,
 * then the default. Live is the default so a bare deploy never shows fake data.
 */
export function resolveConsoleMode(input: { search?: string; env?: string | undefined } = {}): ConsoleMode {
  const q = new URLSearchParams(input.search ?? '').get('console')
  for (const v of [q, input.env]) {
    if (v === 'fixture' || v === 'live') return v
  }
  return 'live'
}
