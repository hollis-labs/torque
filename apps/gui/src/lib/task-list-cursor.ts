const KEY = 'clockwork:task-list-cursor'

export function saveTaskListCursor(ids: string[]): void {
  try {
    sessionStorage.setItem(KEY, JSON.stringify(ids))
  } catch {
    // sessionStorage may be unavailable (private mode / quota) — fail quiet
  }
}

export function readTaskListCursor(): string[] {
  try {
    const raw = sessionStorage.getItem(KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    return parsed.filter((v): v is string => typeof v === 'string')
  } catch {
    return []
  }
}

export function clearTaskListCursor(): void {
  try {
    sessionStorage.removeItem(KEY)
  } catch {
    // ignore
  }
}
