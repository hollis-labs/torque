/** @vitest-environment jsdom */
import { afterEach, expect, it } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { FixtureSource } from '../events/fixture-source'
import { ConsoleProvider } from '../events/provider'
import { createCardRegistry } from '../cards/registry'
import { GENERIC_KIND } from '../cards/kinds'
import { FixtureMetricsSource } from './fixtures'
import { fleetMetricsFromEvents } from './fleet'
import { FleetMetricsPanel, ScopeMetricsPanel } from './panels'
import { MetricsSourceProvider } from './metrics-source-context'
import { METRIC_CARD_KINDS, registerMetricCardKinds } from './card-kinds'
import { fakeTorqueClient, fleetEvents, sprintTasks, TORQUE_NOW } from './torque-fixtures'
import { deriveBurn, parseScope, progressFromCounts, TorqueMetricsSource } from './torque-source'

afterEach(cleanup)
const opts = { now: () => TORQUE_NOW }

it('parses scopes', () => {
  expect(parseScope('epic:EP-1')).toEqual({ kind: 'epic', id: 'EP-1' })
  expect(parseScope('tag:ws-x')).toEqual({ kind: 'tag', id: 'ws-x' })
  expect(parseScope('bogus')).toBeNull()
})

it('maps torque statuses to segments and drops cancelled/archived', () => {
  const segs = progressFromCounts({ done: 3, doing: 2, todo: 1, backlog: 2, paused: 1, cancelled: 4, archived: 9, blocked: 1 })
  expect(Object.fromEntries(segs.map((s) => [s.status, s.count]))).toEqual({ done: 3, in_progress: 2, blocked: 1, todo: 4 })
})

it('loads an epic from rollup and derives burn from task timestamps', async () => {
  const client = fakeTorqueClient()
  const m = await new TorqueMetricsSource(client, opts).load('sprint:SP-1')
  expect(m.origin).toBe('torque')
  expect(m.progress.find((s) => s.status === 'done')?.count).toBe(8)
  expect(client.calls).toContain('rollup:sprint_id')
  // 16 live tasks; 6 created after the sprint started (7, 8, 10, 12, 15, 16)
  expect(m.burn.total).toBe(10)
  expect(m.burn.scopeChanges?.reduce((n, c) => n + c.delta, 0)).toBe(6)
  expect(m.burn.completed.at(-1)?.v).toBe(8)
  expect(m.burnQuality).toBe('derived')
  expect(m.notes?.some((n) => n.includes('approximate'))).toBe(true)
  expect(m.tiles.find((t) => t.id === 'blocked')?.value).toBe(1)
  expect(m.tiles.find((t) => t.id === 'tasks_done_7d')?.value).toBe(6)
})

it('counts tag scopes from the list and pages through cursors', async () => {
  const client = fakeTorqueClient({ pageSize: 5 })
  const m = await new TorqueMetricsSource(client, opts).load('tag:ws-agent-console')
  expect(client.calls.filter((c) => c === 'list').length).toBe(4)
  expect(client.calls.some((c) => c.startsWith('rollup'))).toBe(false)
  expect(m.progress.reduce((n, s) => n + s.count, 0)).toBe(16)
  expect(m.notes?.some((n) => n.includes('rollup unavailable'))).toBe(true)
})

it('flags truncated task lists', async () => {
  const m = await new TorqueMetricsSource(fakeTorqueClient({ pageSize: 2 }), { ...opts, pageSize: 2, maxPages: 2 }).load('epic:EP-1')
  expect(m.notes?.some((n) => n.includes('first 4 tasks'))).toBe(true)
})

it('falls back to the fixture, marked, when Torque fails; throws without a fallback', async () => {
  const failing = fakeTorqueClient({ failWith: 'boom' })
  const m = await new TorqueMetricsSource(failing, { ...opts, fallback: new FixtureMetricsSource() }).load('epic:EP-1')
  expect(m.origin).toBe('fixture')
  expect(m.notes?.[0]).toContain('boom')
  await expect(new TorqueMetricsSource(failing, opts).load('epic:EP-1')).rejects.toThrow('boom')
})

it('handles empty scopes and open-ended sprints', async () => {
  const m = await new TorqueMetricsSource(fakeTorqueClient({ tasks: [] }), opts).load('epic:EP-1')
  expect(m.progress).toEqual([])
  expect(m.burnQuality).toBe('unavailable')
  const b = deriveBurn(sprintTasks(), TORQUE_NOW - 9 * 86_400_000, TORQUE_NOW)
  expect(b.total).toBeGreaterThan(0)
})

it('panel renders progress, derived-burn caveat, and honest empty/error states', async () => {
  const src = new TorqueMetricsSource(fakeTorqueClient(), opts)
  render(<MetricsSourceProvider source={src}><ScopeMetricsPanel scope="sprint:SP-1" now={TORQUE_NOW} /></MetricsSourceProvider>)
  await waitFor(() => expect(screen.getByText('burn approximate')).toBeTruthy())
  expect(screen.getAllByText(/approximate: Torque records no completion times/).length).toBe(1)
  cleanup()
  render(<MetricsSourceProvider source={new TorqueMetricsSource(fakeTorqueClient({ tasks: [] }), opts)}><ScopeMetricsPanel scope="epic:EP-1" /></MetricsSourceProvider>)
  await waitFor(() => expect(screen.getByText(/nothing to chart/)).toBeTruthy())
  cleanup()
  render(<MetricsSourceProvider source={new TorqueMetricsSource(fakeTorqueClient({ failWith: 'down' }), opts)}><ScopeMetricsPanel scope="epic:EP-1" /></MetricsSourceProvider>)
  await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('down'))
})

it('folds agent.state events into counts over time and tick series', () => {
  const m = fleetMetricsFromEvents(fleetEvents(), { now: TORQUE_NOW })
  expect(m.counts).toEqual({ working: 3, idle: 1, waiting: 0 })
  expect(m.total).toBe(4)
  expect(m.series.working.points.length).toBe(24)
  expect(m.series.working.points[0].v).toBe(0)
  expect(m.tokens?.points.length).toBe(6)
  expect(m.cost).toBeNull()
  expect(fleetMetricsFromEvents([], { now: TORQUE_NOW }).hasData).toBe(false)
})

it('fleet panel renders from the console store and shows empty state', async () => {
  const s = new FixtureSource({ intervalMs: 0, initial: 0, families: false })
  fleetEvents().forEach((e) => s.emit(e))
  render(<ConsoleProvider source={s} resyncMs={0}><FleetMetricsPanel now={TORQUE_NOW} /></ConsoleProvider>)
  await waitFor(() => expect(screen.getByText('working')).toBeTruthy())
  expect(screen.getByText('tokens / min')).toBeTruthy()
  cleanup()
  render(<ConsoleProvider source={new FixtureSource({ intervalMs: 0, initial: 0, families: false })} resyncMs={0}><FleetMetricsPanel now={TORQUE_NOW} /></ConsoleProvider>)
  await waitFor(() => expect(screen.getByText('No agent activity in this window')).toBeTruthy())
})

it('registers metric card kinds', () => {
  const reg = createCardRegistry(GENERIC_KIND)
  const off = registerMetricCardKinds(reg)
  expect(reg.get(METRIC_CARD_KINDS.burndown)).toBeTruthy()
  off()
  expect(reg.get(METRIC_CARD_KINDS.burndown)).toBeUndefined()
})
