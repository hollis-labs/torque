import { useState } from 'react'
import { Check, CircleAlert, Pencil, Plus, Trash2, X } from 'lucide-react'
import { useApi } from '@/hooks/use-api'
import { notifyError } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { Subtodo } from '@/lib/types'

interface SubtodosPanelProps {
  taskId: string
  subtodos: Subtodo[]
  onChange: (next: Subtodo[]) => void
}

interface DraftEdit {
  text: string
  required: boolean
}

export function SubtodosPanel({ taskId, subtodos, onChange }: SubtodosPanelProps) {
  const api = useApi()
  const [pending, setPending] = useState<string | null>(null)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editDraft, setEditDraft] = useState<DraftEdit>({ text: '', required: false })
  const [adding, setAdding] = useState(false)
  const [addDraft, setAddDraft] = useState<DraftEdit>({ text: '', required: false })

  const total = subtodos.length
  const done = subtodos.filter((s) => s.done).length
  const allDone = total > 0 && done === total
  const requiredOpen = subtodos.some((s) => s.required && !s.done)

  async function handleMark(item: Subtodo) {
    if (item.done || pending) return
    setPending(item.id)
    try {
      const updated = await api.markSubtodoDone(taskId, item.id)
      onChange(updated)
    } catch (err) {
      notifyError(err, 'Failed to mark subtodo done')
    } finally {
      setPending(null)
    }
  }

  function startEdit(item: Subtodo) {
    setEditingId(item.id)
    setEditDraft({ text: item.text, required: item.required })
  }

  function cancelEdit() {
    setEditingId(null)
    setEditDraft({ text: '', required: false })
  }

  async function handleSaveEdit(itemID: string) {
    if (!editDraft.text.trim()) return
    setPending(itemID)
    try {
      const updated = await api.updateSubtodo(taskId, itemID, {
        text: editDraft.text.trim(),
        required: editDraft.required,
      })
      onChange(updated)
      cancelEdit()
    } catch (err) {
      notifyError(err, 'Failed to update subtodo')
    } finally {
      setPending(null)
    }
  }

  async function handleDelete(itemID: string) {
    setPending(itemID)
    try {
      const updated = await api.deleteSubtodo(taskId, itemID)
      onChange(updated)
      if (editingId === itemID) cancelEdit()
    } catch (err) {
      notifyError(err, 'Failed to delete subtodo')
    } finally {
      setPending(null)
    }
  }

  async function handleAddSubmit() {
    const text = addDraft.text.trim()
    if (!text) return
    setPending('__new__')
    try {
      const updated = await api.addSubtodo(taskId, {
        // Caller-supplied unique id — timestamp is good enough for human-driven adds.
        id: `ui-${Date.now().toString(36)}`,
        text,
        required: addDraft.required,
      })
      onChange(updated)
      setAdding(false)
      setAddDraft({ text: '', required: false })
    } catch (err) {
      notifyError(err, 'Failed to add subtodo')
    } finally {
      setPending(null)
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-3">
        <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">Subtodos</span>
        {total > 0 && (
          <span
            className={cn(
              'font-mono text-[11px] tabular-nums',
              allDone ? 'text-emerald-400' : 'text-zinc-300',
            )}
          >
            {done}/{total}
          </span>
        )}
        {requiredOpen && (
          <span
            className="inline-flex items-center gap-1 text-[10px] uppercase tracking-[.14em] text-amber-400"
            title="Required items remain — task is gated from review until they're checked off."
          >
            <CircleAlert className="h-3 w-3" aria-hidden />
            Gate open
          </span>
        )}
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => setAdding(true)}
          disabled={adding}
          className="ml-auto h-6 gap-1 text-[10px] uppercase tracking-wider"
        >
          <Plus className="h-3 w-3" aria-hidden />
          Add
        </Button>
      </div>

      {total === 0 && !adding && (
        <p className="text-[13px] text-zinc-500">
          No checklist yet. Use Add to create one.
        </p>
      )}

      {(total > 0 || adding) && (
        <ul className="flex flex-col divide-y divide-zinc-800/60 rounded-md border border-zinc-800/60 bg-zinc-950">
          {subtodos.map((item) => {
            const busy = pending === item.id
            const isEditing = editingId === item.id
            return (
              <li key={item.id} className="flex items-start gap-3 px-3 py-2">
                <button
                  type="button"
                  onClick={() => handleMark(item)}
                  disabled={item.done || busy || isEditing}
                  aria-checked={item.done}
                  role="checkbox"
                  aria-label={`Mark ${item.text} done`}
                  className={cn(
                    'mt-0.5 flex h-4 w-4 flex-none items-center justify-center rounded border transition-colors',
                    item.done
                      ? 'border-emerald-600 bg-emerald-600 text-zinc-950'
                      : 'border-zinc-700 bg-zinc-900 hover:border-zinc-500',
                    busy && 'opacity-50',
                  )}
                >
                  {item.done && <Check className="h-3 w-3" aria-hidden />}
                </button>
                <div className="min-w-0 flex-1">
                  {isEditing ? (
                    <SubtodoEditRow
                      draft={editDraft}
                      onDraft={setEditDraft}
                      onSave={() => handleSaveEdit(item.id)}
                      onCancel={cancelEdit}
                      busy={busy}
                    />
                  ) : (
                    <div className="flex items-center gap-2">
                      <span
                        className={cn(
                          'text-[13px]',
                          item.done ? 'text-zinc-500 line-through' : 'text-zinc-200',
                        )}
                      >
                        {item.text}
                      </span>
                      {item.required && !item.done && (
                        <span className="text-[9px] uppercase tracking-[.18em] text-amber-400/80">
                          required
                        </span>
                      )}
                    </div>
                  )}
                  {!isEditing && item.evidence && (
                    <div className="mt-0.5 font-mono text-[11px] text-zinc-500 break-all">
                      {item.evidence}
                    </div>
                  )}
                </div>
                {!isEditing && (
                  <div className="flex flex-none items-center gap-1">
                    <button
                      type="button"
                      onClick={() => startEdit(item)}
                      disabled={busy}
                      aria-label={`Edit ${item.text}`}
                      className="rounded p-1 text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200 disabled:opacity-50"
                    >
                      <Pencil className="h-3 w-3" aria-hidden />
                    </button>
                    <button
                      type="button"
                      onClick={() => handleDelete(item.id)}
                      disabled={busy}
                      aria-label={`Delete ${item.text}`}
                      className="rounded p-1 text-zinc-500 hover:bg-zinc-800 hover:text-rose-400 disabled:opacity-50"
                    >
                      <Trash2 className="h-3 w-3" aria-hidden />
                    </button>
                  </div>
                )}
              </li>
            )
          })}
          {adding && (
            <li className="flex items-start gap-3 px-3 py-2">
              <span className="mt-0.5 h-4 w-4 flex-none rounded border border-zinc-800 bg-zinc-900" />
              <div className="min-w-0 flex-1">
                <SubtodoEditRow
                  draft={addDraft}
                  onDraft={setAddDraft}
                  onSave={handleAddSubmit}
                  onCancel={() => {
                    setAdding(false)
                    setAddDraft({ text: '', required: false })
                  }}
                  busy={pending === '__new__'}
                  placeholder="New subtodo…"
                  autoFocus
                />
              </div>
            </li>
          )}
        </ul>
      )}
    </div>
  )
}

function SubtodoEditRow({
  draft,
  onDraft,
  onSave,
  onCancel,
  busy,
  placeholder,
  autoFocus,
}: {
  draft: DraftEdit
  onDraft: (next: DraftEdit) => void
  onSave: () => void
  onCancel: () => void
  busy: boolean
  placeholder?: string
  autoFocus?: boolean
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Input
        value={draft.text}
        onChange={(e) => onDraft({ ...draft, text: e.target.value })}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault()
            onSave()
          } else if (e.key === 'Escape') {
            e.preventDefault()
            onCancel()
          }
        }}
        placeholder={placeholder}
        autoFocus={autoFocus}
        disabled={busy}
        className="h-7 text-[13px]"
      />
      <div className="flex items-center gap-2">
        <label className="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-zinc-500">
          <input
            type="checkbox"
            checked={draft.required}
            onChange={(e) => onDraft({ ...draft, required: e.target.checked })}
            disabled={busy}
            className="h-3 w-3"
          />
          Required
        </label>
        <div className="ml-auto flex items-center gap-1">
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={onCancel}
            disabled={busy}
            className="h-6 gap-1 text-[10px] uppercase tracking-wider"
          >
            <X className="h-3 w-3" aria-hidden />
            Cancel
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={onSave}
            disabled={busy || !draft.text.trim()}
            className="h-6 gap-1 text-[10px] uppercase tracking-wider"
          >
            <Check className="h-3 w-3" aria-hidden />
            {busy ? 'Saving…' : 'Save'}
          </Button>
        </div>
      </div>
    </div>
  )
}

/**
 * Compact `done/total` chip for the TasksTable. Green when every item is
 * checked, zinc otherwise. Returns null when the task has no checklist so
 * the column stays clean for the many tasks that never use subtodos.
 */
export function SubtodosBadge({ subtodos }: { subtodos: Subtodo[] | undefined }) {
  if (!subtodos || subtodos.length === 0) return null
  const done = subtodos.filter((s) => s.done).length
  const total = subtodos.length
  const allDone = done === total
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-sm border px-1 py-0 font-mono text-[10px] leading-4 tabular-nums',
        allDone
          ? 'border-emerald-700/50 bg-emerald-900/30 text-emerald-300'
          : 'border-zinc-700/70 bg-zinc-900 text-zinc-300',
      )}
      title={`${done} of ${total} checklist items done`}
    >
      {done}/{total}
    </span>
  )
}
