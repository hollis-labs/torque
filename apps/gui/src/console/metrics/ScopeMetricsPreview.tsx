import { useMemo } from 'react'
import { FixtureSource } from '../events/fixture-source'
import { ConsoleProvider } from '../events/provider'
import { fleetEvents, fakeTorqueClient, TORQUE_NOW } from './torque-fixtures'
import { FleetMetricsPanel, ScopeMetricsPanel } from './panels'
import { MetricsSourceProvider } from './metrics-source-context'
import { TorqueMetricsSource } from './torque-source'
import { FixtureMetricsSource } from './fixtures'

/** Showcase for WidgetPreviewPage: the Torque-backed views on an in-memory client (no network). */
export function ScopeMetricsPreview() {
  const source = useMemo(() => new TorqueMetricsSource(fakeTorqueClient(), { now: () => TORQUE_NOW, fallback: new FixtureMetricsSource() }), [])
  const failing = useMemo(() => new TorqueMetricsSource(fakeTorqueClient({ failWith: 'connection refused' }), { now: () => TORQUE_NOW, fallback: new FixtureMetricsSource() }), [])
  const empty = useMemo(() => new TorqueMetricsSource(fakeTorqueClient({ tasks: [] }), { now: () => TORQUE_NOW }), [])
  const fleet = useMemo(() => {
    const s = new FixtureSource({ intervalMs: 0, initial: 0, families: false })
    fleetEvents().forEach((e) => s.emit(e))
    return s
  }, [])
  return (
    <div className="space-y-6">
      <MetricsSourceProvider source={source}><ScopeMetricsPanel scope="sprint:SP-1" title="Sprint 1 (Torque)" now={TORQUE_NOW} /></MetricsSourceProvider>
      <div className="grid gap-6 lg:grid-cols-2">
        <MetricsSourceProvider source={failing}><ScopeMetricsPanel scope="epic:EP-1" title="Torque down: fixture fallback" compact now={TORQUE_NOW} /></MetricsSourceProvider>
        <MetricsSourceProvider source={empty}><ScopeMetricsPanel scope="epic:EP-2" title="Empty epic" now={TORQUE_NOW} /></MetricsSourceProvider>
      </div>
      <ConsoleProvider source={fleet} resyncMs={0}><FleetMetricsPanel now={TORQUE_NOW} /></ConsoleProvider>
    </div>
  )
}
