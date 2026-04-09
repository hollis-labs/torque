import { ArrowLeft } from 'lucide-react'
import { Link } from 'react-router-dom'
import { StatusBadge } from './status-badge'

interface DetailHeaderProps {
  title: string
  backTo: string
  backLabel: string
  id: string
  status?: string
  children?: React.ReactNode
}

export function DetailHeader({ title, backTo, backLabel, id, status, children }: DetailHeaderProps) {
  return (
    <div className="border-b border-border bg-card px-6 py-4">
      <div className="flex items-center gap-2 mb-3">
        <Link
          to={backTo}
          className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          {backLabel}
        </Link>
        <span className="text-muted-foreground/40 text-xs">/</span>
        <span className="text-xs text-muted-foreground font-mono">{id}</span>
      </div>

      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-2 min-w-0">
          <h1 className="text-xl font-semibold text-foreground leading-tight">{title}</h1>
          <div className="flex items-center gap-2 flex-wrap">
            {status && <StatusBadge status={status} />}
            {children}
          </div>
        </div>
      </div>
    </div>
  )
}
