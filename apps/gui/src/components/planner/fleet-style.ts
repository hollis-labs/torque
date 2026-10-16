import type { AgentState } from '@/lib/agent-activity'
import type { FleetState } from '@/lib/fleet-agents'

export const FLEET_STATE_STYLE: Record<FleetState, { label: string; dot: string; text: string; fill: string; avatar: AgentState }> = {
  waiting: { label: 'WAITING ON YOU', dot: 'bg-amber-400', text: 'text-amber-300', fill: '#fbbf24', avatar: 'blocked' },
  busy: { label: 'WORKING', dot: 'bg-emerald-400', text: 'text-emerald-300', fill: '#34d399', avatar: 'working' },
  idle: { label: 'IDLE', dot: 'bg-sky-400', text: 'text-sky-300', fill: '#38bdf8', avatar: 'idle' },
  unknown: { label: 'UNKNOWN', dot: 'bg-zinc-600', text: 'text-zinc-500', fill: '#52525b', avatar: 'idle' },
}
