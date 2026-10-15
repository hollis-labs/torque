import { useEffect, useRef, useState } from 'react'
import type { AgentActivity, AgentHistoryEntry } from '@/lib/agent-activity'

// TODO(agent-activity): replace the demo feed with the real API once the data
// model is final. Keep the return shape; the page only reads `agents` and `live`.

const MIN = 60_000
const BUCKETS = 24
const TICK_MS = 4_000

interface Seed {
  id: string
  name: string
  role: string
  group: string | null
  state: AgentActivity['state']
  agoMin: number
  last: string
  done: number
  failed: number
  streak: number
}

const SEEDS: Seed[] = [
  { id: 'agt-cogsworth', name: 'Cogsworth', role: 'implementer', group: 'WS-torque-gui', state: 'working', agoMin: 1, last: 'Edited agent-activity.tsx', done: 7, failed: 1, streak: 4 },
  { id: 'agt-pixel-pete', name: 'Pixel Pete', role: 'reviewer', group: 'WS-torque-gui', state: 'idle', agoMin: 18, last: 'Approved PR #262', done: 5, failed: 0, streak: 5 },
  { id: 'agt-bolt', name: 'Bolt', role: 'implementer', group: 'WS-torque-gui', state: 'blocked', agoMin: 42, last: 'Waiting on checkpoint', done: 2, failed: 2, streak: 0 },
  { id: 'agt-gizmo', name: 'Gizmo', role: 'implementer', group: 'WS-tether-forums', state: 'working', agoMin: 3, last: 'Ran migration tests', done: 9, failed: 1, streak: 6 },
  { id: 'agt-rusty', name: 'Rusty', role: 'investigator', group: 'WS-tether-forums', state: 'idle', agoMin: 95, last: 'Filed investigation dossier', done: 3, failed: 0, streak: 3 },
  { id: 'agt-sprocket', name: 'Sprocket', role: 'planner', group: 'WS-tesseract', state: 'working', agoMin: 6, last: 'Promoted draft to knowledge', done: 4, failed: 0, streak: 4 },
  { id: 'agt-widget', name: 'Widget', role: 'implementer', group: null, state: 'offline', agoMin: 400, last: 'Session ended', done: 1, failed: 0, streak: 1 },
]

const ACTIONS = ['Ran go test ./...', 'Edited a file', 'Read the AGENTS.md', 'Searched Tesseract', 'Committed changes', 'Posted a comment', 'Opened a checkpoint', 'Transitioned a task']

/** Deterministic pseudo-noise so the demo is stable between renders. */
function wave(seedStr: string, i: number): number {
  let h = 0
  for (let k = 0; k < seedStr.length; k++) h = (h * 31 + seedStr.charCodeAt(k)) | 0
  return Math.abs(Math.sin(h + i * 1.7)) * 8
}

function build(seed: Seed, now: number): AgentActivity {
  const active = seed.state === 'working' ? 1 : seed.state === 'idle' ? 0.5 : 0.1
  const series = Array.from({ length: BUCKETS }, (_, i) => Math.round(wave(seed.id, i) * active * (0.4 + i / BUCKETS)))
  const history = Array.from({ length: 8 }, (_, i): AgentHistoryEntry => ({
    at: now - (seed.agoMin + i * 23) * MIN,
    kind: i === 0 ? 'note' : i % 5 === 3 ? 'failed' : i % 3 === 0 ? 'started' : 'done',
    text: i === 0 ? seed.last : ACTIONS[(i + seed.name.length) % ACTIONS.length],
  }))
  return {
    id: seed.id,
    name: seed.name,
    role: seed.role,
    group: seed.group,
    state: seed.state,
    lastActionAt: now - seed.agoMin * MIN,
    lastAction: seed.last,
    stats: {
      done24h: seed.done,
      failed24h: seed.failed,
      actions24h: series.reduce((s, n) => s + n, 0),
      streak: seed.streak,
      avgTaskMin: seed.done ? 12 + (seed.name.length % 5) * 7 : null,
    },
    series,
    history,
  }
}

/** A looping demo feed: every few seconds one agent acts, so the board re-sorts. */
export function useAgentActivity(): { agents: AgentActivity[]; live: boolean; now: number } {
  const [now, setNow] = useState(() => Date.now())
  const [agents, setAgents] = useState<AgentActivity[]>(() => SEEDS.map((s) => build(s, Date.now())))
  const step = useRef(0)

  useEffect(() => {
    const t = setInterval(() => {
      const at = Date.now()
      setNow(at)
      const n = step.current++
      setAgents((prev) =>
        prev.map((a, i) => {
          if (i !== (n * 3 + 1) % prev.length || a.state === 'offline') return a
          const text = ACTIONS[n % ACTIONS.length]
          const finished = n % 3 === 0
          const series = [...a.series]
          series[series.length - 1] += 1
          return {
            ...a,
            state: a.state === 'blocked' ? 'working' : a.state === 'idle' ? 'working' : a.state,
            lastActionAt: at,
            lastAction: text,
            series,
            stats: {
              ...a.stats,
              actions24h: a.stats.actions24h + 1,
              done24h: a.stats.done24h + (finished ? 1 : 0),
              streak: finished ? a.stats.streak + 1 : a.stats.streak,
            },
            history: [{ at, kind: finished ? ('done' as const) : ('note' as const), text }, ...a.history].slice(0, 30),
          }
        }),
      )
    }, TICK_MS)
    return () => clearInterval(t)
  }, [])

  return { agents, live: false, now }
}
