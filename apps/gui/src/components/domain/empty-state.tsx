import { LayoutList, SearchX, AlertCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'

type EmptyVariant = 'no-tasks' | 'no-results' | 'error'

interface EmptyStateProps {
  variant?: EmptyVariant
  title?: string
  description?: string
  action?: {
    label: string
    onClick: () => void
  }
}

const DEFAULTS: Record<EmptyVariant, { icon: React.ReactNode; title: string; description: string }> = {
  'no-tasks': {
    icon: <LayoutList className="h-10 w-10 text-muted-foreground/50" />,
    title: 'No tasks yet',
    description: 'Create your first task to get started.',
  },
  'no-results': {
    icon: <SearchX className="h-10 w-10 text-muted-foreground/50" />,
    title: 'No results found',
    description: 'Try adjusting your filters or search query.',
  },
  'error': {
    icon: <AlertCircle className="h-10 w-10 text-destructive/60" />,
    title: 'Something went wrong',
    description: 'Failed to load data. Please try again.',
  },
}

export function EmptyState({ variant = 'no-tasks', title, description, action }: EmptyStateProps) {
  const defaults = DEFAULTS[variant]

  return (
    <div className="flex flex-col items-center justify-center gap-3 py-16 text-center">
      {defaults.icon}
      <div className="space-y-1">
        <p className="text-sm font-medium text-foreground">{title ?? defaults.title}</p>
        <p className="text-sm text-muted-foreground">{description ?? defaults.description}</p>
      </div>
      {action && (
        <Button variant="outline" size="sm" onClick={action.onClick} className="mt-2">
          {action.label}
        </Button>
      )}
    </div>
  )
}
