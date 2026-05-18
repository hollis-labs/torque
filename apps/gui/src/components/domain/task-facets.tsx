import { CollapsibleSection } from '@hollis-labs/sysop-ui'
import type { Task, TemplateRef } from '@/lib/types'

interface TaskFacetsProps {
  task: Task
}

export function TaskFacets({ task }: TaskFacetsProps) {
  const templateRef = readTemplateRef(task.metadata)

  return (
    <CollapsibleSection label="Facets" accent="green" collapsible={false}>
      <dl className="grid grid-cols-3 gap-x-4 gap-y-3">
        <FacetField label="Kind">
          <Chip>{task.kind}</Chip>
        </FacetField>

        <FacetField label="Source">
          <div className="flex flex-wrap items-center gap-1">
            <Chip>{task.source_type}</Chip>
            {task.source_ref && (
              <span
                className="text-[11px] font-mono text-zinc-400 break-all"
                title={task.source_ref}
              >
                {task.source_ref}
              </span>
            )}
          </div>
        </FacetField>

        <FacetField label="Trust">
          <Chip tone={trustTone(task.trust)}>{task.trust}</Chip>
        </FacetField>

        <FacetField label="Checkpoint Mode">
          <Chip>{task.checkpoint_mode}</Chip>
        </FacetField>

        <FacetField label="On Checkpoint Response">
          <Chip>{task.on_checkpoint_response}</Chip>
        </FacetField>

        {templateRef && (
          <FacetField label="Template Ref">
            <span className="text-[12px] font-mono text-zinc-300 break-all">
              {templateRef.id}
              <span className="text-zinc-600">@v{templateRef.version}</span>
            </span>
          </FacetField>
        )}
      </dl>
    </CollapsibleSection>
  )
}

function readTemplateRef(metadata: Record<string, unknown> | null | undefined): TemplateRef | null {
  if (!metadata) return null
  const raw = (metadata as Record<string, unknown>).template_ref
  if (!raw || typeof raw !== 'object') return null
  const obj = raw as Record<string, unknown>
  const id = typeof obj.id === 'string' ? obj.id : ''
  const version = typeof obj.version === 'number' ? obj.version : Number(obj.version)
  if (!id || !Number.isFinite(version)) return null
  return { id, version }
}

function trustTone(trust: string): ChipTone {
  if (trust === 'trusted') return 'green'
  if (trust === 'untrusted') return 'red'
  return 'zinc'
}

type ChipTone = 'zinc' | 'green' | 'red'

function Chip({ children, tone = 'zinc' }: { children: React.ReactNode; tone?: ChipTone }) {
  const classes: Record<ChipTone, string> = {
    zinc: 'border-zinc-800 bg-zinc-900 text-zinc-300',
    green: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-200',
    red: 'border-red-500/40 bg-red-500/10 text-red-200',
  }
  return (
    <span
      className={`inline-flex items-center rounded border px-1.5 py-0.5 text-[11px] font-mono ${classes[tone]}`}
    >
      {children}
    </span>
  )
}

function FacetField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1 min-w-0">
      <dt className="text-[9px] uppercase tracking-[.18em] text-zinc-600">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  )
}
