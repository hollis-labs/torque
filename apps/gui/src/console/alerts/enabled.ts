/**
 * The shell mounts the Console only when it was asked for: an explicit
 * `?console=fixture|live` or VITE_CONSOLE_SOURCE=fixture|live. Otherwise the app
 * behaves exactly as before (no provider, no streams, no alerts UI).
 */
export function isConsoleEnabled(input: { search?: string; env?: string | undefined } = {}): boolean {
  const q = new URLSearchParams(input.search ?? '').get('console')
  return [q, input.env].some((v) => v === 'fixture' || v === 'live')
}
