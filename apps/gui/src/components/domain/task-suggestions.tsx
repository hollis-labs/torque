import { useCallback, useEffect, useState } from 'react'
import { Check, Sparkles, X } from 'lucide-react'
import { Button, CollapsibleSection } from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { pct, questionLabel } from '@/lib/judge'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { JudgeStatus, Suggestion, Task } from '@/lib/types'

interface TaskSuggestionsProps {
  taskId: string
  /** Called with the refreshed task after tags were added, so the page shows them. */
  onApplied: (task: Task) => void
}

/**
 * Suggested tags for one task. Suggestion-only: nothing changes until Accept,
 * which adds the tags; Reject just closes the suggestion. Renders nothing when
 * there is nothing to show and no way to ask for suggestions.
 */
export function TaskSuggestions({ taskId, onApplied }: TaskSuggestionsProps) {
  const api = useApi()
  const [items, setItems] = useState<Suggestion[]>([])
  const [status, setStatus] = useState<JudgeStatus | null>(null)
  const [busy, setBusy] = useState<number | 'ask' | null>(null)

  const load = useCallback(async () => {
    try {
      setItems(await api.judgeSuggestions({ task_id: taskId }))
    } catch {
      setItems([]) // suggestions are optional: a failed read hides the panel
    }
  }, [api, taskId])

  useEffect(() => {
    void load()
    api.judgeStatus().then(setStatus).catch(() => setStatus(null))
  }, [api, load])

  async function accept(s: Suggestion) {
    setBusy(s.id)
    try {
      const res = await api.judgeAccept(s.id)
      notifySuccess(res.added_tags.length ? `Added ${res.added_tags.join(', ')}` : 'Already tagged')
      onApplied(await api.getTask(taskId))
      await load()
    } catch (err) {
      notifyError(err, 'Failed to accept')
    } finally {
      setBusy(null)
    }
  }

  async function reject(s: Suggestion) {
    setBusy(s.id)
    try {
      await api.judgeReject(s.id)
      await load()
    } catch (err) {
      notifyError(err, 'Failed to reject')
    } finally {
      setBusy(null)
    }
  }

  async function ask() {
    setBusy('ask')
    try {
      const res = await api.judgeEnrich(taskId)
      if (res.suggestions === 0) notifySuccess('Nothing to suggest: the tags already cover it, or the model was unsure')
      await load()
    } catch (err) {
      notifyError(err, 'Failed to get suggestions')
    } finally {
      setBusy(null)
    }
  }

  const canAsk = status?.decider_configured === true
  if (items.length === 0 && !canAsk) return null

  return (
    <CollapsibleSection label="Suggested tags" accent="neutral" collapsible={false}>
      <div className="flex flex-col gap-2">
        {items.length === 0 && <span className="text-[13px] italic text-zinc-600">No open suggestions.</span>}
        {items.map((s) => (
          <div key={s.id} className="flex items-center gap-2 text-[13px]" data-testid="suggestion">
            <Sparkles className="h-3.5 w-3.5 shrink-0 text-zinc-500" aria-hidden />
            <span className="flex flex-wrap gap-1">
              {s.suggested.map((t) => (
                <span key={t} className="rounded bg-zinc-800 px-1.5 py-0.5 font-mono text-[11px] text-zinc-200">
                  {t}
                </span>
              ))}
            </span>
            <span className="text-zinc-500" title={`${s.provider} ${s.model}`}>
              {pct(s.confidence)} · {questionLabel(s.set_id, s.question_key)}
            </span>
            <span className="ml-auto flex gap-1">
              <Button size="sm" variant="outline" className="h-7 gap-1" disabled={busy !== null} onClick={() => void accept(s)} aria-label={`Accept ${s.suggested.join(', ')}`}>
                <Check className="h-3.5 w-3.5" />
                Accept
              </Button>
              <Button size="sm" variant="ghost" className="h-7 gap-1" disabled={busy !== null} onClick={() => void reject(s)} aria-label={`Reject ${s.suggested.join(', ')}`}>
                <X className="h-3.5 w-3.5" />
                Reject
              </Button>
            </span>
          </div>
        ))}
        {canAsk && (
          <div>
            <Button size="sm" variant="ghost" className="h-7 gap-1" disabled={busy !== null} onClick={() => void ask()}>
              <Sparkles className="h-3.5 w-3.5" />
              {busy === 'ask' ? 'Asking…' : 'Suggest tags'}
            </Button>
          </div>
        )}
      </div>
    </CollapsibleSection>
  )
}
