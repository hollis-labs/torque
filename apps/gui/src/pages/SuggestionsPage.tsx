import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { Check, X } from 'lucide-react'
import { Button, EmptyState, PageHeader, Skeleton } from '@hollis-labs/sysop-ui'
import { FilterSelect } from '@/components/domain/filter-select'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { acceptanceRate, groupByBand, pct, questionLabel, type Band } from '@/lib/judge'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { JudgeStatus, JudgeSummary, Suggestion } from '@/lib/types'

const SSE_EVENTS = ['judge.changed']
/** The most the server will decide in one bulk call. */
const BULK_LIMIT = 500

/**
 * Triage for tag suggestions. Suggestion-only: nothing is tagged until a
 * person accepts it, one at a time or a confidence band at a time. Every
 * decision is recorded in the ledger.
 */
export default function SuggestionsPage() {
  const api = useApi()
  const [items, setItems] = useState<Suggestion[] | null>(null)
  const [status, setStatus] = useState<JudgeStatus | null>(null)
  const [summary, setSummary] = useState<JudgeSummary>({})
  const [error, setError] = useState<string | null>(null)
  const [question, setQuestion] = useState('') // "set/key" or all
  const [confirm, setConfirm] = useState<string | null>(null) // "accept:<band>" awaiting a second click
  const [busy, setBusy] = useState(false)
  const { lastEvent } = useSSE(SSE_EVENTS)

  const load = useCallback(async () => {
    try {
      const [list, st, sum] = await Promise.all([api.judgeSuggestions({ limit: BULK_LIMIT }), api.judgeStatus(), api.judgeSummary()])
      setItems(list)
      setStatus(st)
      setSummary(sum)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load suggestions')
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])
  useEffect(() => {
    if (lastEvent) void load()
  }, [lastEvent, load])

  const questions = useMemo(() => {
    const seen = new Map<string, number>()
    for (const s of items ?? []) seen.set(`${s.set_id}/${s.question_key}`, (seen.get(`${s.set_id}/${s.question_key}`) ?? 0) + 1)
    return [...seen.entries()].sort()
  }, [items])

  const visible = useMemo(() => (items ?? []).filter((s) => !question || `${s.set_id}/${s.question_key}` === question), [items, question])
  const groups = useMemo(() => groupByBand(visible), [visible])

  async function decideOne(s: Suggestion, accept: boolean) {
    setBusy(true)
    try {
      if (accept) await api.judgeAccept(s.id)
      else await api.judgeReject(s.id)
      await load()
    } catch (err) {
      notifyError(err, 'Failed')
    } finally {
      setBusy(false)
    }
  }

  async function decideBand(band: Band, action: 'accept' | 'reject', count: number) {
    const key = `${action}:${band.key}`
    if (confirm !== key) {
      setConfirm(key) // accepting tags many tasks at once: ask again
      return
    }
    setConfirm(null)
    setBusy(true)
    try {
      const [set, q] = question ? question.split('/') : [undefined, undefined]
      const res = await api.judgeBulk({ action, set, question: q, min: band.min, max: band.max, limit: Math.min(count, BULK_LIMIT) })
      notifySuccess(`${action === 'accept' ? 'Accepted' : 'Rejected'} ${res.decided}${res.failed?.length ? `, ${res.failed.length} failed` : ''}`)
      await load()
    } catch (err) {
      notifyError(err, 'Failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex h-full min-w-0 flex-col">
      <PageHeader title="Suggestions" />
      <div className="min-w-0 flex-1 overflow-y-auto">
        <p className="border-b border-zinc-800/80 px-4 py-3 text-[13px] text-zinc-400">
          Tags a model proposes for tasks that don’t have them. Nothing changes until you accept.{' '}
          {status && (
            <span className="text-zinc-500">
              {status.active
                ? `New tasks are suggested automatically (${status.decider}).`
                : status.decider_configured
                  ? 'Automatic suggestions are off (turn on the “judge” feature in Settings).'
                  : 'No model is connected to this server; suggestions come from backfill runs.'}
            </span>
          )}
        </p>

        {error && <p role="alert" className="border-b border-zinc-800/80 px-4 py-3 text-[13px] text-red-400">{error}</p>}
        {items === null && !error && <Skeleton className="h-24 w-full" />}

        {items !== null && items.length === 0 && (
          <EmptyState
            variant="empty"
            title="No open suggestions"
            description="Run `torque judge backfill --set task-kind --decider tether --estimate` to see what a backfill would ask and cost."
          />
        )}

        {questions.length > 1 && (
          <label className="flex flex-wrap items-center gap-2 border-b border-zinc-800/80 px-4 py-3 text-[12px] text-zinc-400">
            Question
            <FilterSelect
              value={question}
              onChange={(e) => {
                setQuestion(e.target.value)
                setConfirm(null)
              }}
            >
              <option value="">All ({items?.length ?? 0})</option>
              {questions.map(([k, n]) => {
                const [set, key] = k.split('/')
                return (
                  <option key={k} value={k}>
                    {questionLabel(set, key)} ({n})
                  </option>
                )
              })}
            </FilterSelect>
          </label>
        )}

        {groups.map(({ band, items: rows }) => (
          <section key={band.key} className="min-w-0 border-b border-zinc-800/80" aria-label={`Confidence ${band.label}`}>
            <div className="flex flex-wrap items-center gap-2 border-b border-zinc-800/80 bg-zinc-900/20 px-4 py-3">
              <h2 className="text-[10px] uppercase tracking-[.18em] text-zinc-400">
                {band.label} · {rows.length}
              </h2>
              <span className="ml-auto flex gap-1">
                <Button size="sm" variant="outline" className="h-7" disabled={busy} onClick={() => void decideBand(band, 'accept', rows.length)}>
                  {confirm === `accept:${band.key}` ? `Confirm: tag ${rows.length} tasks` : `Accept all ${rows.length}`}
                </Button>
                <Button size="sm" variant="ghost" className="h-7" disabled={busy} onClick={() => void decideBand(band, 'reject', rows.length)}>
                  {confirm === `reject:${band.key}` ? `Confirm: reject ${rows.length}` : `Reject all ${rows.length}`}
                </Button>
              </span>
            </div>
            <ul>
              {rows.map((s) => (
                <li key={s.id} className="flex min-w-0 flex-wrap items-center gap-3 border-b border-zinc-800/60 px-4 py-3 text-[13px] last:border-b-0" data-testid="suggestion-row">
                  <Link to={`/tasks/${s.subject_id}`} className="min-w-0 basis-full [overflow-wrap:anywhere] text-zinc-200 sm:flex-1 sm:basis-0 hover:underline" title={s.task_title}>
                    <span className="mr-2 font-mono text-[11px] text-zinc-500">{s.subject_id}</span>
                    {s.task_title ?? s.subject_id}
                  </Link>
                  <span className="hidden min-w-0 max-w-full flex-wrap gap-1 sm:flex sm:max-w-48">
                    {(s.task_tags ?? []).map((t) => (
                      <span key={t} className="max-w-full [overflow-wrap:anywhere] rounded bg-zinc-900 px-1 font-mono text-[10px] text-zinc-500">{t}</span>
                    ))}
                  </span>
                  <span className="flex min-w-0 max-w-full flex-wrap gap-1 sm:max-w-64">
                    {s.suggested.map((t) => (
                      <span key={t} className="max-w-full [overflow-wrap:anywhere] rounded bg-zinc-800 px-1.5 py-0.5 font-mono text-[11px] text-zinc-100">+ {t}</span>
                    ))}
                  </span>
                  <span className="ml-auto w-10 shrink-0 text-right text-zinc-500">{pct(s.confidence)}</span>
                  <Button size="sm" variant="outline" className="h-7 w-7 shrink-0 p-0" disabled={busy} onClick={() => void decideOne(s, true)} aria-label={`Accept ${s.suggested.join(', ')} for ${s.subject_id}`}>
                    <Check className="h-3.5 w-3.5" />
                  </Button>
                  <Button size="sm" variant="ghost" className="h-7 w-7 shrink-0 p-0" disabled={busy} onClick={() => void decideOne(s, false)} aria-label={`Reject ${s.suggested.join(', ')} for ${s.subject_id}`}>
                    <X className="h-3.5 w-3.5" />
                  </Button>
                </li>
              ))}
            </ul>
          </section>
        ))}

        <Rates summary={summary} />
      </div>
    </div>
  )
}

/** How often each question's suggestions are accepted: how a question earns its place. */
function Rates({ summary }: { summary: JudgeSummary }) {
  const rows = Object.entries(summary).flatMap(([set, qs]) =>
    Object.entries(qs).map(([key, counts]) => ({ set, key, rate: acceptanceRate(counts), open: counts.suggested ?? 0 })),
  )
  if (rows.every((r) => r.rate === null)) return null
  return (
    <section className="border-b border-zinc-800/80" aria-label="Acceptance rates">
      <h2 className="border-b border-zinc-800/80 bg-zinc-900/20 px-4 py-3 text-[10px] uppercase tracking-[.18em] text-zinc-400">Accepted so far</h2>
      <ul className="text-[13px] text-zinc-400">
        {rows
          .filter((r) => r.rate !== null)
          .map((r) => (
            <li key={`${r.set}/${r.key}`} className="border-b border-zinc-800/60 px-4 py-3 [overflow-wrap:anywhere] last:border-b-0">
              {questionLabel(r.set, r.key)}: {pct(r.rate!)}
            </li>
          ))}
      </ul>
    </section>
  )
}
