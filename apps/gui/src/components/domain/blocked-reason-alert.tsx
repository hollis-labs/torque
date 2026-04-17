import { AlertCircle, PauseCircle } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { TaskStatus } from '@/lib/types'

const VARIANT_CLASSES: Record<'blocked' | 'paused', string> = {
  blocked:
    'border-red-500/40 bg-red-500/10 text-red-200 [&_[data-icon]]:text-red-300',
  paused:
    'border-yellow-500/40 bg-yellow-500/10 text-yellow-200 [&_[data-icon]]:text-yellow-300',
}

const VARIANT_LABEL: Record<'blocked' | 'paused', string> = {
  blocked: 'Blocked',
  paused: 'Paused',
}

interface BlockedReasonAlertProps {
  status: Extract<TaskStatus, 'blocked' | 'paused'>
  reason: string
  className?: string
}

export function BlockedReasonAlert({ status, reason, className }: BlockedReasonAlertProps) {
  const Icon = status === 'blocked' ? AlertCircle : PauseCircle
  return (
    <div
      role="alert"
      aria-label={`${VARIANT_LABEL[status]} reason`}
      className={cn(
        'flex gap-3 border px-4 py-3',
        VARIANT_CLASSES[status],
        className,
      )}
      data-testid="blocked-reason-alert"
    >
      <Icon data-icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="text-[10px] font-medium uppercase tracking-[.18em]">
          {VARIANT_LABEL[status]}
        </div>
        <p className="whitespace-pre-wrap break-words text-[12px] leading-relaxed text-zinc-100">
          {reason}
        </p>
      </div>
    </div>
  )
}
