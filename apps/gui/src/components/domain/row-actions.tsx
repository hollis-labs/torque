import { MoreHorizontal } from 'lucide-react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

export interface RowAction {
  label: string
  onClick: () => void
  variant?: 'default' | 'destructive'
}

interface RowActionsProps {
  actions: RowAction[]
}

export function RowActions({ actions }: RowActionsProps) {
  const normal = actions.filter((a) => a.variant !== 'destructive')
  const destructive = actions.filter((a) => a.variant === 'destructive')

  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="inline-flex h-7 w-7 items-center justify-center rounded-md text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors">
        <MoreHorizontal className="h-4 w-4" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {normal.map((action) => (
          <DropdownMenuItem key={action.label} onClick={action.onClick}>
            {action.label}
          </DropdownMenuItem>
        ))}
        {destructive.length > 0 && <DropdownMenuSeparator />}
        {destructive.map((action) => (
          <DropdownMenuItem
            key={action.label}
            onClick={action.onClick}
            className="text-red-400 focus:text-red-300"
          >
            {action.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
