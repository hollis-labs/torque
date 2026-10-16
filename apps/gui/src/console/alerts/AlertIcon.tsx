import { AlertTriangle, Bot, CheckSquare, GitPullRequest, Info, MessageSquare, OctagonAlert, ShieldQuestion, type LucideIcon } from 'lucide-react'
import { SEVERITY_STYLE } from './severity-style'
import type { AlertIconKey, AlertSeverity } from './types'

const ICONS: Record<AlertIconKey, LucideIcon> = {
  info: Info,
  task: CheckSquare,
  agent: Bot,
  message: MessageSquare,
  approval: ShieldQuestion,
  pr: GitPullRequest,
  warning: AlertTriangle,
  error: OctagonAlert,
}

export function AlertIcon({ icon, severity }: { icon: AlertIconKey; severity: AlertSeverity }) {
  const I = ICONS[icon]
  return <I aria-hidden="true" className={`h-4 w-4 shrink-0 ${SEVERITY_STYLE[severity].text}`} />
}
