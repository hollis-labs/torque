/** @vitest-environment jsdom */
import { afterEach, expect, it } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { Burndown, FixtureMetricsSource, MetricsPreview, ProgressBar, Sparkline, StackedBar, StatTile, estimateEta, fixtureBurn, formatEta, segmentShares, seriesFromTicks } from './index'
import { completedAt, delta, scopeAt } from './math'

afterEach(cleanup)
const NOW = Date.parse('2026-10-16T12:00:00Z')

it('segment shares sum to 100 and handle zero', () => {
  expect(segmentShares([{ status: 'a', count: 1 }, { status: 'b', count: 1 }, { status: 'c', count: 1 }]).reduce((a, b) => a + b, 0)).toBe(100)
  expect(segmentShares([{ status: 'a', count: 0 }])).toEqual([0])
})

it('estimates and formats ETA', () => {
  const start = NOW - 10 * 3_600_000
  expect(estimateEta(0, 5, start, NOW)).toBeNull()
  expect(estimateEta(5, 5, start, NOW)).toBe(NOW + 10 * 3_600_000)
  expect(formatEta(null, NOW)).toBe('ETA unknown')
  expect(formatEta(NOW + 10 * 3_600_000, NOW)).toBe('ETA ~10h')
  expect(formatEta(NOW, NOW)).toBe('complete')
})

it('burn helpers apply scope changes and completions', () => {
  const b = fixtureBurn(NOW)
  expect(scopeAt(b, b.startTs)).toBe(20)
  expect(scopeAt(b, b.endTs)).toBe(21)
  expect(completedAt(b, b.startTs - 1)).toBe(0)
  expect(completedAt(b, NOW)).toBe(12)
})

it('delta and tick folding', () => {
  expect(delta(10, 0)).toEqual({ abs: 10, pct: null })
  expect(delta(10)).toBeNull()
  const s = seriesFromTicks([
    { ts: 2, payload: { metric: 'q', value: 4, unit: 'n' } },
    { ts: 1, payload: { metric: 'q', value: 3 } },
    { ts: 3, payload: { metric: 'other', value: 9 } },
    { ts: 4, payload: { metric: 'q', value: 'bad' } },
  ], 'q')
  expect(s.points).toEqual([{ t: 1, v: 3 }, { t: 2, v: 4 }])
  expect(s.unit).toBe('n')
})

it('ProgressBar exposes progressbar semantics and degenerate states', () => {
  const { rerender } = render(<ProgressBar segments={[{ status: 'done', count: 3 }, { status: 'todo', count: 1 }]} startTs={NOW - 3_600_000} now={NOW} />)
  const bar = screen.getByRole('progressbar')
  expect(bar.getAttribute('aria-valuenow')).toBe('3')
  expect(bar.getAttribute('aria-valuemax')).toBe('4')
  expect(screen.getByText(/ETA/)).toBeTruthy()
  rerender(<ProgressBar segments={[]} />)
  expect(screen.queryByRole('progressbar')).toBeNull()
  expect(screen.getByText('No tasks')).toBeTruthy()
  rerender(<ProgressBar segments={[]} loading />)
  expect(screen.getByRole('status').getAttribute('aria-busy')).toBe('true')
})

it('StackedBar labels segments', () => {
  render(<StackedBar label="Runs" segments={[{ status: 'done', count: 3 }, { status: 'blocked', count: 1, label: 'Failed' }]} />)
  expect(screen.getByRole('img').getAttribute('aria-label')).toBe('Runs: Done 3, Failed 1')
})

it('Sparkline handles empty, single and many points', () => {
  const { rerender } = render(<Sparkline label="s" points={[]} />)
  expect(screen.getByRole('img').getAttribute('aria-label')).toBe('s: no data')
  rerender(<Sparkline label="s" points={[{ t: 1, v: 2 }]} />)
  expect(screen.getByRole('img').querySelector('path')).toBeNull()
  rerender(<Sparkline label="s" points={[{ t: 1, v: 2 }, { t: 2, v: 3 }, { t: 3, v: Number.NaN }]} />)
  expect(screen.getByRole('img').querySelector('path')).not.toBeNull()
})

it('StatTile shows delta direction with inverted tone', () => {
  render(<StatTile label="Failed" value={4} previous={2} invert />)
  expect(screen.getByText(/\+2 \(\+100%\)/).className).toContain('rose')
  cleanup()
  render(<StatTile label="Done" value={null} />)
  expect(screen.getByText('–')).toBeTruthy()
})

it('Burndown renders both modes, scope markers, and empty data', () => {
  const { rerender } = render(<Burndown data={fixtureBurn(NOW)} now={NOW} />)
  expect(screen.getByRole('img').getAttribute('aria-label')).toMatch(/12 of 21 done, 9 remaining, 2 scope changes/)
  expect(screen.getAllByText(/Added CI follow-ups/).length).toBeGreaterThan(0)
  rerender(<Burndown data={fixtureBurn(NOW)} now={NOW} mode="burnup" />)
  expect(screen.getByText('Scope')).toBeTruthy()
  const b = fixtureBurn(NOW)
  rerender(<Burndown data={{ ...b, completed: [] }} now={b.startTs} />)
  expect(screen.getByRole('img')).toBeTruthy()
  rerender(<Burndown data={{ ...b, total: 0, scopeChanges: [] }} />)
  expect(screen.getByText('No scope to chart')).toBeTruthy()
})

it('FixtureMetricsSource supports empty and failure; preview resolves', async () => {
  expect((await new FixtureMetricsSource({ empty: true }).load()).progress).toEqual([])
  await expect(new FixtureMetricsSource({ fail: 'boom' }).load()).rejects.toThrow('boom')
  render(<MetricsPreview />)
  await waitFor(() => expect(screen.getByLabelText('Epic progress')).toBeTruthy())
})
