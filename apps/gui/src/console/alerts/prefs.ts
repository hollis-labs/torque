// Alert preferences now live on the ConsolePrefs seam (../prefs). These exports
// keep A1's names and behavior: an AlertPrefsStore IS a ConsolePrefsStore, and
// the localStorage key is unchanged (new fields default when reading old entries).

import { CONSOLE_PREFS_STORAGE_KEY, LocalConsolePrefsStore, MemoryConsolePrefsStore } from '../prefs/store'
import type { ConsolePrefsStore } from '../prefs/types'

export type AlertPrefsStore = ConsolePrefsStore

export class MemoryAlertPrefsStore extends MemoryConsolePrefsStore {}

export const ALERT_PREFS_STORAGE_KEY = CONSOLE_PREFS_STORAGE_KEY

export class LocalAlertPrefsStore extends LocalConsolePrefsStore {}
