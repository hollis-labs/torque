import type { TaskBrief } from '@/lib/types'
import type { KindResolver, RefEntity, ResolveOutcome, StatusTone } from './types'

export interface MockResolverOptions {
  latencyMs?: number
  /** Canonicals that make the whole batch throw. */
  failFor?: readonly string[]
  /** Inspect calls in tests: each entry is one resolveMany batch. */
  onCall?: (canonicals: string[]) => void
}

/** A KindResolver backed by a fixture map; anything not in it is not-found. */
export function createMockResolver(fixtures: Record<string, RefEntity>, opts: MockResolverOptions = {}): KindResolver {
  return {
    async resolveMany(canonicals, { signal }) {
      opts.onCall?.([...canonicals])
      if (opts.latencyMs) {
        await new Promise<void>((resolve, reject) => {
          const t = setTimeout(resolve, opts.latencyMs)
          signal.addEventListener('abort', () => {
            clearTimeout(t)
            reject(new DOMException('aborted', 'AbortError'))
          })
        })
      }
      if (opts.failFor && canonicals.some((c) => opts.failFor!.includes(c))) throw new Error('mock resolver failure')
      const out: Record<string, ResolveOutcome> = {}
      for (const c of canonicals) out[c] = fixtures[c] ? { status: 'found', entity: fixtures[c] } : { status: 'not-found' }
      return out
    },
  }
}

const TASK_TONE: Record<string, StatusTone> = {
  done: 'success',
  in_progress: 'info',
  review: 'info',
  blocked: 'danger',
  failed: 'danger',
  todo: 'neutral',
}

/**
 * The real adapter for `torque.task`: wraps the existing
 * `api.taskTitles`, so task
 * references resolve against the live Torque with no new backend work.
 */
export function createTorqueTaskResolver(
  fetchTitles: (ids: string[], signal?: AbortSignal) => Promise<Record<string, TaskBrief | null>>,
): KindResolver {
  return {
    async resolveMany(ids, { signal }) {
      const titles = await fetchTitles(ids, signal)
      const out: Record<string, ResolveOutcome> = {}
      for (const id of ids) {
        const t = titles[id]
        out[id] = t
          ? {
              status: 'found',
              entity: {
                title: t.title,
                status: { label: t.status.replace(/_/g, ' '), tone: TASK_TONE[t.status] ?? 'neutral' },
                fields: [{ label: 'Priority', value: `P${t.priority}` }],
                updatedAt: t.updated_at,
              },
            }
          : { status: 'not-found' }
      }
      return out
    },
  }
}

/** Fixtures covering the built-in kinds, for tests and the widget preview page. */
export const MOCK_ENTITIES: Record<string, Record<string, RefEntity>> = {
  'torque.task': {
    'CW-20261016-0101': { title: 'Reference registry + pattern detector', status: { label: 'in progress', tone: 'info' }, fields: [{ label: 'Priority', value: 'P1' }] },
  },
  'torque.epic': { 'EP-20261016-0002': { title: 'Agent Console GUI', status: { label: 'active', tone: 'info' } } },
  'torque.project': { 'PRJ-20260416-0001': { title: 'Torque' } },
  workstream: { 'ws-agent-console': { title: 'Agent Console', subtitle: 'Workstream' } },
  agent: { 'claude-code': { title: 'claude-code', status: { label: 'busy', tone: 'info' } } },
  'tesseract.key': {
    'project/torque/workspace/handoff#agent_console_gui_orchestrator_2026_10_16': { title: 'Handoff: Agent Console GUI (orchestrator brief)', subtitle: 'workspace' },
  },
}

export function createMockResolvers(opts: MockResolverOptions = {}): Record<string, KindResolver> {
  return Object.fromEntries(Object.entries(MOCK_ENTITIES).map(([kind, fx]) => [kind, createMockResolver(fx, opts)]))
}
