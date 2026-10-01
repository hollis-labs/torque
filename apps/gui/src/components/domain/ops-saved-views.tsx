import { useState } from 'react'
import { Button } from '@hollis-labs/sysop-ui'
import type { OpsSavedView } from '@/lib/ops-saved-views'

interface Props {
  views: OpsSavedView[]
  activeId: string
  onSelect: (id: string) => void
  onApply: () => void
  onReset: () => void
  onSaveAs: (name: string) => void
  onSaveOver: () => void
  modified: boolean
}
export function OpsSavedViews({ views, activeId, onSelect, onApply, onReset, onSaveAs, onSaveOver, modified }: Props) {
  const [name, setName] = useState('')
  return <div className="flex flex-wrap items-center gap-2 text-xs">
    <select aria-label="Saved view" value={activeId} onChange={event => onSelect(event.target.value)}
      className="h-8 max-w-44 rounded border border-border bg-panel px-2">
      <option value="">Saved views</option>
      {views.map(view => <option key={view.id} value={view.id}>{view.name}</option>)}
    </select>
    <Button variant="outline" size="sm" onClick={onApply} disabled={!activeId}>Apply</Button>
    <Button variant="outline" size="sm" onClick={onReset}>Reset</Button>
    <input aria-label="New view name" value={name} onChange={event => setName(event.target.value)}
      placeholder="View name" className="h-8 w-36 rounded border border-border bg-panel px-2" />
    <Button variant="outline" size="sm" disabled={!name.trim()} onClick={() => { onSaveAs(name.trim()); setName('') }}>Save as</Button>
    <Button variant="outline" size="sm" disabled={!activeId || !modified} onClick={onSaveOver}>Save over</Button>
    {activeId && modified && <span className="text-text-subtle">Unsaved changes</span>}
  </div>
}
