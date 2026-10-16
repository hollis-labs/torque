import { useState } from 'react'
import { SEVERITY_ORDER } from '../events/types'
import { SEVERITY_STYLE } from '../alerts/severity-style'
import { PREF_FIELD_KEYS, consolePrefsSettingsGroup } from './manifest'
import type { ConsolePrefs } from './types'

const LABEL = 'font-mono text-[10px] uppercase tracking-widest text-zinc-400'
const INPUT =
  'border border-zinc-700 bg-zinc-950 px-1.5 py-1 font-mono text-xs text-zinc-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:opacity-50'

export interface ConsolePrefsPanelProps {
  prefs: ConsolePrefs
  onChange: (patch: Partial<ConsolePrefs>) => void
}

/**
 * In-app preferences form, built on plain controls. Field titles and help text
 * come from the manifest group (consolePrefsSettingsGroup), so swapping this for
 * kit-settings' <SettingsRenderer> changes the chrome, not the data. Edits apply
 * immediately (no draft/save step) because the store is local; see BACKEND_NEEDS.md.
 */
export function ConsolePrefsPanel({ prefs, onChange }: ConsolePrefsPanelProps) {
  const props = consolePrefsSettingsGroup().schema.properties
  const help = (field: keyof typeof PREF_FIELD_KEYS) => String(props[field]?.description ?? '')
  // Muted sources edit as free text; commit on blur so typing a comma is not eaten by normalising.
  const [mutedDraft, setMuted] = useState<string | null>(null)
  const muted = mutedDraft ?? prefs.mutedSources.join(', ')

  return (
    <form aria-label="Alert preferences" className="max-h-72 space-y-3 overflow-y-auto border-b border-zinc-800 px-4 py-3" onSubmit={(e) => e.preventDefault()}>
      <div>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={prefs.quiet} onChange={(e) => onChange({ quiet: e.target.checked })} className="h-3 w-3 accent-amber-300" />
          <span className={LABEL}>Quiet mode</span>
        </label>
        <p className="mt-1 text-[11px] leading-snug text-zinc-500">{help('quiet')}</p>
      </div>

      <div>
        <label className="flex items-center gap-2">
          <span className={LABEL}>Toast threshold</span>
          <select
            aria-label="Toast threshold"
            value={prefs.toastMinSeverity}
            onChange={(e) => onChange({ toastMinSeverity: e.target.value as ConsolePrefs['toastMinSeverity'] })}
            className={INPUT}
          >
            {SEVERITY_ORDER.slice(1).map((s) => (
              <option key={s} value={s}>
                {SEVERITY_STYLE[s].label.toLowerCase()}+
              </option>
            ))}
          </select>
        </label>
      </div>

      <fieldset className="space-y-1.5">
        <legend className="sr-only">Quiet hours</legend>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={prefs.quietHoursEnabled} onChange={(e) => onChange({ quietHoursEnabled: e.target.checked })} className="h-3 w-3 accent-amber-300" />
          <span className={LABEL}>Quiet hours</span>
        </label>
        <div className="flex items-center gap-2">
          <input type="time" aria-label="Quiet hours start" value={prefs.quietHoursStart} disabled={!prefs.quietHoursEnabled} onChange={(e) => e.target.value && onChange({ quietHoursStart: e.target.value })} className={INPUT} />
          <span className={LABEL}>to</span>
          <input type="time" aria-label="Quiet hours end" value={prefs.quietHoursEnd} disabled={!prefs.quietHoursEnabled} onChange={(e) => e.target.value && onChange({ quietHoursEnd: e.target.value })} className={INPUT} />
        </div>
        <p className="text-[11px] leading-snug text-zinc-500">{help('quiet_hours_enabled')}</p>
      </fieldset>

      <div>
        <label className="block">
          <span className={LABEL}>Muted sources</span>
          <input
            type="text"
            value={muted}
            placeholder="agent-bravo, laptop"
            onChange={(e) => setMuted(e.target.value)}
            onBlur={() => {
              if (mutedDraft !== null) onChange({ mutedSources: mutedDraft.split(',') })
              setMuted(null)
            }}
            className={`${INPUT} mt-1 w-full`}
          />
        </label>
        <p className="mt-1 text-[11px] leading-snug text-zinc-500">{help('muted_sources')}</p>
      </div>

      <div>
        <label className="flex items-center gap-2">
          <span className={LABEL}>Coalesce window (s)</span>
          <input
            type="number"
            min={0}
            max={3600}
            value={prefs.coalesceWindowSec}
            onChange={(e) => onChange({ coalesceWindowSec: e.target.valueAsNumber })}
            className={`${INPUT} w-20`}
          />
        </label>
        <p className="mt-1 text-[11px] leading-snug text-zinc-500">{help('coalesce_window_sec')}</p>
      </div>
    </form>
  )
}
