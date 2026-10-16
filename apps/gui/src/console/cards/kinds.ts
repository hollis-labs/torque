import { ApprovalCard, GenericCard, InfoCard, StatusCard } from './kind-components'
import { createCardRegistry, type CardKindDef, type CardRegistry } from './registry'

export const GENERIC_KIND: CardKindDef = { id: 'generic', label: 'Event', render: { expanded: GenericCard } }

export const REFERENCE_KINDS: CardKindDef[] = [
  { id: 'info', label: 'Info', render: { expanded: InfoCard }, priority: 1 },
  {
    id: 'approval',
    label: 'Approval',
    render: { expanded: ApprovalCard },
    actionSchema: [
      { id: 'approve', label: 'Approve', intent: 'primary', kind: 'approve' },
      { id: 'reject', label: 'Reject', intent: 'danger', kind: 'reject' },
    ],
    priority: 10,
  },
  { id: 'status', label: 'Status', render: { expanded: StatusCard, inline: StatusCard }, surfaces: ['tray', 'inline'], priority: 0 },
]

export function createDefaultCardRegistry(): CardRegistry {
  return createCardRegistry(GENERIC_KIND, REFERENCE_KINDS)
}
