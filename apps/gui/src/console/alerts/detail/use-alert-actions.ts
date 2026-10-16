import { useCallback, useEffect, useRef, useState } from 'react'
import { useConsoleSeams } from '../../events/hooks'
import type { ConsoleEvent } from '../../events/types'
import type { ItemStatus, StateOp } from '../../seams/state-store'
import type { Alert } from '../types'
import { ALERT_DECISION_KINDS, CONTEXT_DEFER_FALLBACK_MS, type AlertActionSpec } from './actions'

export interface AlertActionInput {
  /** Absolute epoch ms (snooze / time defer). */
  until?: number
  note?: string
  /** Defer-to-context label. */
  context?: string
}

export interface OptimisticState {
  status: ItemStatus
  snoozeUntil?: number
  resolution?: string
}

export interface AlertActionRunner {
  /** The action currently in flight, or null. A second run while one is in flight is ignored. */
  pending: string | null
  /** Visible failure of the last action (state was rolled back). */
  error: string | null
  /** Visible success of the last action. */
  notice: string | null
  /** Overrides the store status while an action is in flight or just landed, so the UI answers instantly. */
  optimistic: OptimisticState | null
  run(action: AlertActionSpec, input?: AlertActionInput): Promise<boolean>
  restore(): Promise<boolean>
  clearMessages(): void
}

/**
 * Executes the alert action set against the Console seams.
 *   dismiss / snooze / defer / settle: optimistic StateStore op, rolled back with a visible error on failure.
 *   approve / deny: ActionBus dispatch (IdentityConfirm when the spec carries `confirm`); only a granted + ok
 *   result settles the alert, so a declined step-up changes nothing.
 * Idempotent: a re-entrant call while one is pending is ignored, and a StateStore op whose target state is
 * already current is skipped.
 */
export function useAlertActions(alert: Alert, event: ConsoleEvent | undefined, current: { status: ItemStatus }): AlertActionRunner {
  const { stateStore, actionBus } = useConsoleSeams()
  const [pending, setPending] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [optimistic, setOptimistic] = useState<OptimisticState | null>(null)
  const inflight = useRef(false)
  const alive = useRef(true)
  const statusRef = useRef(current.status)
  useEffect(() => {
    statusRef.current = current.status
  })
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  // Once the store catches up, the optimistic override is redundant.
  useEffect(() => {
    if (optimistic && optimistic.status === current.status && !inflight.current) setOptimistic(null)
  }, [current.status, optimistic])

  const guard = useCallback(
    async (name: string, body: () => Promise<string | undefined>): Promise<boolean> => {
      if (inflight.current) return false
      inflight.current = true
      setPending(name)
      setError(null)
      setNotice(null)
      try {
        const msg = await body()
        if (alive.current && msg) setNotice(msg)
        return true
      } catch (e) {
        if (alive.current) {
          setOptimistic(null)
          setError(e instanceof Error ? e.message : 'Action failed')
        }
        return false
      } finally {
        inflight.current = false
        if (alive.current) setPending(null)
      }
    },
    [],
  )

  const store = useCallback(
    async (op: StateOp, next: OptimisticState) => {
      if (statusRef.current === next.status && op.type !== 'snooze') return
      setOptimistic(next)
      await stateStore.apply(op)
    },
    [stateStore],
  )

  const run = useCallback(
    (action: AlertActionSpec, input: AlertActionInput = {}) =>
      guard(action.id, async () => {
        const key = alert.id
        switch (action.id) {
          case 'dismiss':
            await store({ type: 'dismiss', key, note: input.note }, { status: 'dismissed' })
            return 'Dismissed'
          case 'snooze': {
            const until = input.until ?? Date.now() + 3_600_000
            await store({ type: 'snooze', key, until, note: input.note }, { status: 'snoozed', snoozeUntil: until })
            return `Snoozed until ${new Date(until).toLocaleString([], { hour: '2-digit', minute: '2-digit', month: 'short', day: 'numeric' })}`
          }
          case 'defer': {
            const until = input.until ?? Date.now() + CONTEXT_DEFER_FALLBACK_MS
            const note = input.context ? `defer:${input.context}${input.note ? ` | ${input.note}` : ''}` : `defer${input.note ? `: ${input.note}` : ''}`
            await store({ type: 'snooze', key, until, note }, { status: 'snoozed', snoozeUntil: until })
            return input.context ? `Deferred: ${input.context}` : 'Deferred'
          }
          case 'settle':
            await store({ type: 'settle', key, resolution: 'handled', note: input.note }, { status: 'settled', resolution: 'handled' })
            return 'Settled'
          case 'approve':
          case 'deny': {
            const res = await actionBus.dispatch({
              kind: action.id === 'approve' ? ALERT_DECISION_KINDS.approve : ALERT_DECISION_KINDS.deny,
              actionId: action.id,
              eventId: event?.id ?? alert.id,
              subject: event?.subjects.find((s) => s.type !== 'agent') ?? event?.subjects[0],
              params: input.note ? { note: input.note } : undefined,
              confirm: action.confirm,
            })
            if (res.status !== 'ok') throw new Error(res.status === 'denied' ? `Not confirmed: ${res.message ?? 'identity check declined'}` : (res.message ?? `${action.label} failed`))
            const resolution = action.id === 'approve' ? 'approved' : 'rejected'
            await store({ type: 'settle', key: alert.id, resolution, note: input.note }, { status: 'settled', resolution })
            return res.message ?? (action.id === 'approve' ? 'Approved' : 'Denied')
          }
          case 'reply':
            return undefined // handled by the composer panel
        }
      }),
    [guard, store, alert.id, event, actionBus],
  )

  const restore = useCallback(
    () =>
      guard('restore', async () => {
        setOptimistic({ status: 'unread' })
        await stateStore.apply({ type: 'reopen', key: alert.id })
        return 'Restored'
      }),
    [guard, stateStore, alert.id],
  )

  return { pending, error, notice, optimistic, run, restore, clearMessages: () => (setError(null), setNotice(null)) }
}
