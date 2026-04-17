import { useMemo } from 'react'
import { cn } from '@/lib/utils'
import type { Run, Task } from '@/lib/types'

export interface ActivityHeatmapProps {
  tasks: Task[]
  runs: Run[]
  weekCount?: number
  className?: string
}

const HEAT_STYLES: Array<{ className?: string; style?: React.CSSProperties }> = [
  { className: 'bg-muted/40' },
  { style: { backgroundColor: 'var(--color-status-doing)', opacity: 0.35 } },
  { style: { backgroundColor: 'var(--color-status-doing)', opacity: 0.65 } },
  { style: { backgroundColor: 'var(--color-status-done)', opacity: 0.75 } },
  { style: { backgroundColor: 'var(--color-status-done)', opacity: 1 } },
]

function heatLevel(count: number, max: number): number {
  if (count === 0 || max === 0) return 0
  const pct = count / max
  if (pct < 0.15) return 1
  if (pct < 0.4) return 2
  if (pct < 0.7) return 3
  return 4
}

export function ActivityHeatmap({
  tasks,
  runs,
  weekCount = 16,
  className,
}: ActivityHeatmapProps) {
  const activityMap = useMemo(() => {
    const map = new Map<string, number>()
    for (const t of tasks) {
      if (!t.updated_at) continue
      const d = t.updated_at.slice(0, 10)
      map.set(d, (map.get(d) ?? 0) + 1)
    }
    for (const r of runs) {
      if (!r.started_at) continue
      const d = r.started_at.slice(0, 10)
      map.set(d, (map.get(d) ?? 0) + 1)
    }
    return map
  }, [tasks, runs])

  const maxCount = useMemo(() => {
    let m = 0
    for (const v of activityMap.values()) if (v > m) m = v
    return m || 1
  }, [activityMap])

  const grid = useMemo(() => {
    const today = new Date()
    const todayStr = today.toISOString().slice(0, 10)

    const currentSunday = new Date(today)
    currentSunday.setDate(today.getDate() - today.getDay())
    currentSunday.setHours(0, 0, 0, 0)

    const gridStart = new Date(currentSunday)
    gridStart.setDate(gridStart.getDate() - (weekCount - 1) * 7)

    const weeks: { date: string; future: boolean }[][] = []
    for (let w = 0; w < weekCount; w++) {
      const week: { date: string; future: boolean }[] = []
      for (let d = 0; d < 7; d++) {
        const date = new Date(gridStart)
        date.setDate(gridStart.getDate() + w * 7 + d)
        const ds = date.toISOString().slice(0, 10)
        week.push({ date: ds, future: ds > todayStr })
      }
      weeks.push(week)
    }
    return weeks
  }, [weekCount])

  const monthLabels = useMemo(() => {
    const labels: { weekIdx: number; month: string }[] = []
    let lastMonth = ''
    grid.forEach((week, wi) => {
      const m = week[0].date.slice(0, 7)
      if (m !== lastMonth) {
        lastMonth = m
        labels.push({
          weekIdx: wi,
          month: new Date(`${week[0].date}T12:00:00`).toLocaleString('en', { month: 'short' }),
        })
      }
    })
    return labels
  }, [grid])

  const totalActivity = useMemo(
    () => Array.from(activityMap.values()).reduce((a, b) => a + b, 0),
    [activityMap]
  )

  return (
    <div className={cn('select-none', className)} aria-label="Activity heatmap">
      <div className="mb-1 flex" style={{ paddingLeft: 20 }}>
        {grid.map((_, wi) => {
          const label = monthLabels.find((l) => l.weekIdx === wi)
          return (
            <div
              key={wi}
              className="w-[14px] shrink-0 font-mono text-[9px] text-muted-foreground/70"
            >
              {label ? label.month : ''}
            </div>
          )
        })}
      </div>

      <div className="flex gap-0">
        <div className="mr-1 flex flex-col" style={{ gap: 3 }}>
          {['S', 'M', 'T', 'W', 'T', 'F', 'S'].map((d, i) => (
            <div
              key={i}
              className="flex h-[10px] w-[14px] items-center justify-end font-mono text-[8px] text-muted-foreground/60"
              aria-hidden
            >
              {i % 2 === 1 ? d : ''}
            </div>
          ))}
        </div>

        <div className="flex" style={{ gap: 3 }}>
          {grid.map((week, wi) => (
            <div key={wi} className="flex flex-col" style={{ gap: 3 }}>
              {week.map((cell, di) => {
                if (cell.future) {
                  return <div key={di} className="h-[10px] w-[10px] rounded-sm opacity-0" />
                }
                const count = activityMap.get(cell.date) ?? 0
                const level = heatLevel(count, maxCount)
                const { className: lvlClass, style: lvlStyle } = HEAT_STYLES[level]
                return (
                  <div
                    key={`${wi}-${di}`}
                    title={`${cell.date}: ${count} events`}
                    className={cn('h-[10px] w-[10px] rounded-sm', lvlClass)}
                    style={lvlStyle}
                  />
                )
              })}
            </div>
          ))}
        </div>
      </div>

      <div className="mt-2 flex items-center justify-between">
        <span className="font-mono text-[9px] text-muted-foreground/70">
          {totalActivity} total events
        </span>
        <div className="flex items-center gap-1">
          <span className="font-mono text-[9px] text-muted-foreground/70">less</span>
          {HEAT_STYLES.map(({ className, style }, i) => (
            <div
              key={i}
              className={cn('h-[10px] w-[10px] rounded-sm', className)}
              style={style}
            />
          ))}
          <span className="font-mono text-[9px] text-muted-foreground/70">more</span>
        </div>
      </div>
    </div>
  )
}
