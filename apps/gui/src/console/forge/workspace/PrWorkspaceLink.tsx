import type { ReactNode } from 'react'
import { useAutolink } from '../../autolink/context'
import type { PrRef } from '../types'
import { prWorkspacePath } from './route'

/**
 * Link to a PR's workspace for inboxes and drawers. Uses the autolink host's
 * client-side navigation (and closes an open drawer); falls back to a plain
 * href outside the app shell.
 */
export function PrWorkspaceLink({ prRef, file, children, className }: { prRef: PrRef; file?: string; children?: ReactNode; className?: string }) {
  const { navigate, closeDrawer } = useAutolink()
  const to = prWorkspacePath(prRef, { file })
  return (
    <a
      href={to}
      className={className}
      data-testid="open-pr-workspace"
      onClick={(e) => {
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return
        e.preventDefault()
        closeDrawer()
        navigate(to)
      }}
    >
      {children ?? 'Open workspace'}
    </a>
  )
}
