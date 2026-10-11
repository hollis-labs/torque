import { useEffect, useMemo, useState } from 'react'
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@hollis-labs/sysop-ui'
import { PlannerFields } from '@/components/planner/planner-fields'
import { useApi } from '@/hooks/use-api'
import { usePlannerSchema } from '@/hooks/use-planner-schema'
import { ApiError } from '@/lib/api'
import { buildNewItem, createDraft, createSpecs, missingRequired, plannerMeta, validationErrors, type Draft, type FieldError } from '@/lib/planner'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { PlannerDb, PlannerItem } from '@/lib/types'

interface PlannerCreateDialogProps {
  db: PlannerDb
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The rank a new priority starts at. */
  nextRank?: number
  onCreated: (item: PlannerItem) => void
}

export function PlannerCreateDialog({ db, open, onOpenChange, nextRank = 1, onCreated }: PlannerCreateDialogProps) {
  const api = useApi()
  const schema = usePlannerSchema(db)
  const meta = plannerMeta(db)
  const specs = useMemo(() => (schema ? createSpecs(schema) : []), [schema])
  const [draft, setDraft] = useState<Draft>({})
  const [errors, setErrors] = useState<FieldError[]>([])
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open && schema) {
      setDraft(createDraft(db, specs, nextRank))
      setErrors([])
    }
    // Reseed when the dialog opens or the schema arrives, not on every keystroke.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, schema, db])

  const missing = missingRequired(specs, draft)

  async function submit() {
    if (missing.length > 0) return
    setSubmitting(true)
    setErrors([])
    try {
      const created = await api.plannerCreate(db, buildNewItem(specs, draft))
      notifySuccess(`Created ${created.id}`)
      onOpenChange(false)
      onCreated(created)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'invalid') setErrors(validationErrors(err.details))
      notifyError(err, `Failed to create ${meta.singular}`)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>New {meta.singular}</DialogTitle>
          <DialogDescription>Fill in the essentials. Everything else can be edited once it exists.</DialogDescription>
        </DialogHeader>
        {schema ? (
          <PlannerFields specs={specs} draft={draft} errors={errors} idPrefix="pc" onChange={(name, value) => setDraft((d) => ({ ...d, [name]: value }))} />
        ) : (
          <p className="text-sm text-muted-foreground">Loading…</p>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!schema || submitting || missing.length > 0} title={missing.length ? `Needs: ${missing.join(', ')}` : undefined}>
            {submitting ? 'Creating…' : `Create ${meta.singular}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
