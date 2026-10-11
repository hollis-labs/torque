import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Switch, Textarea } from '@hollis-labs/sysop-ui'
import type { Draft, FieldError, FieldSpec } from '@/lib/planner'

const NONE = '__none__'

interface PlannerFieldsProps {
  specs: FieldSpec[]
  draft: Draft
  onChange: (name: string, value: string) => void
  /** Server validation failures, shown under the field they name. */
  errors?: FieldError[]
  idPrefix?: string
}

/** A form built from the server schema: one control per field, by kind. */
export function PlannerFields({ specs, draft, onChange, errors = [], idPrefix = 'pf' }: PlannerFieldsProps) {
  return (
    <div className="flex flex-col gap-4">
      {specs.map((s) => {
        const id = `${idPrefix}-${s.name}`
        const value = draft[s.name] ?? ''
        const fieldErrors = errors.filter((e) => e.path === s.name || e.path.startsWith(`${s.name}[`))
        return (
          <div key={s.name} className="flex flex-col gap-1.5">
            <Label htmlFor={id}>
              {s.label}
              {s.required ? ' *' : ''}
            </Label>
            {s.kind === 'text' && <Input id={id} value={value} onChange={(e) => onChange(s.name, e.target.value)} />}
            {s.kind === 'longtext' && <Textarea id={id} value={value} rows={s.name === 'tldr' ? 2 : 4} onChange={(e) => onChange(s.name, e.target.value)} />}
            {s.kind === 'list' && (
              <Textarea id={id} value={value} rows={3} placeholder="One per line" onChange={(e) => onChange(s.name, e.target.value)} />
            )}
            {s.kind === 'int' && (
              <Input
                id={id}
                type="number"
                min={s.min}
                max={s.max}
                value={value}
                onChange={(e) => onChange(s.name, e.target.value)}
              />
            )}
            {s.kind === 'bool' && (
              <div>
                <Switch id={id} checked={value === 'true'} onCheckedChange={(v) => onChange(s.name, v ? 'true' : 'false')} aria-label={s.label} />
              </div>
            )}
            {s.kind === 'enum' && (
              <Select value={value === '' ? NONE : value} onValueChange={(v) => onChange(s.name, v === null || v === NONE ? '' : v)}>
                <SelectTrigger id={id} className="h-8 w-full text-sm" aria-label={s.label}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {!s.required && <SelectItem value={NONE}>(none)</SelectItem>}
                  {(s.options ?? []).map((o) => (
                    <SelectItem key={o} value={o}>
                      {o}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            {s.hint && fieldErrors.length === 0 && <p className="text-xs text-muted-foreground">{s.hint}</p>}
            {fieldErrors.map((e, i) => (
              <p key={i} className="text-xs text-destructive">
                {e.message}
              </p>
            ))}
          </div>
        )
      })}
    </div>
  )
}
