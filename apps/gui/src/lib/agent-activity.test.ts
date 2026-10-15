import { describe, expect, it } from 'vitest'
import { UNGROUPED, groupAgents, leaderboardScore, robotSprite, type AgentActivity } from '@/lib/agent-activity'

const NOW = 1_000_000_000
const agent = (over: Partial<AgentActivity> & { id: string }): AgentActivity => ({
  name: over.id,
  group: 'WS-a',
  state: 'idle',
  lastActionAt: NOW,
  lastAction: 'x',
  stats: { done24h: 0, failed24h: 0, actions24h: 0, streak: 0, avgTaskMin: null },
  series: [],
  history: [],
  ...over,
})

describe('agent leaderboard', () => {
  it('ranks finished work above idle chatter and penalises failures', () => {
    const doer = agent({ id: 'doer', stats: { done24h: 5, failed24h: 0, actions24h: 10, streak: 5, avgTaskMin: 10 } })
    const failer = agent({ id: 'failer', stats: { done24h: 5, failed24h: 4, actions24h: 10, streak: 0, avgTaskMin: 10 } })
    expect(leaderboardScore(doer, NOW)).toBeGreaterThan(leaderboardScore(failer, NOW))
  })

  it('groups by workstream, sorts rows by score and puts unassigned agents last', () => {
    const groups = groupAgents(
      [
        agent({ id: 'loose', group: null, stats: { done24h: 99, failed24h: 0, actions24h: 0, streak: 0, avgTaskMin: null } }),
        agent({ id: 'low', group: 'WS-a' }),
        agent({ id: 'high', group: 'WS-a', stats: { done24h: 3, failed24h: 0, actions24h: 0, streak: 0, avgTaskMin: null } }),
      ],
      NOW,
    )
    expect(groups.map((g) => g.name)).toEqual(['WS-a', UNGROUPED])
    expect(groups[0].agents.map((a) => [a.id, a.rank])).toEqual([['high', 1], ['low', 2]])
  })

  it('gives an agent the same face every time', () => {
    expect(robotSprite('agt-1')).toEqual(robotSprite('agt-1'))
  })
})
