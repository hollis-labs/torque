import { PauseCircle } from 'lucide-react'
import { Callout } from '@hollis-labs/sysop-ui'
import type { TaskStatus } from '@/lib/types'

const VARIANT_LABEL: Record<'blocked' | 'paused', string> = {
  blocked: 'Blocked',
  paused: 'Paused',
}

interface BlockedReasonAlertProps {
  status: Extract<TaskStatus, 'blocked' | 'paused'>
  reason: string
  className?: string
}

/**
 * Blocked / paused reason banner — a thin domain wrapper over the kit's
 * `Callout`. `blocked` maps to the danger tone, `paused` to warning (with a
 * pause icon in place of the default warning triangle).
 */
export function BlockedReasonAlert({ status, reason, className }: BlockedReasonAlertProps) {
  return (
    <Callout
      tone={status === 'blocked' ? 'danger' : 'warning'}
      title={VARIANT_LABEL[status]}
      icon={status === 'paused' ? <PauseCircle /> : undefined}
      className={className}
    >
      <p className="whitespace-pre-wrap break-words">{reason}</p>
    </Callout>
  )
}
