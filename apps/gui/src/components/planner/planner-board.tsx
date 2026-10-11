import { Link } from 'react-router-dom'
import { statusTone } from '@hollis-labs/sysop-ui'
import { BOARD_SECTIONS, boardAge, boardMore, boardRowRoute, boardWord } from '@/lib/planner'
import type { PlannerBoard, PlannerBoardRow, PlannerBoardSectionKey } from '@/lib/types'

interface PlannerBoardViewProps {
  board: PlannerBoard
  /** The moment ages are measured from; the board's own generation time by default. */
  nowMs?: number
}

/** The departure board: four sections, one row per line. */
export function PlannerBoardView({ board, nowMs }: PlannerBoardViewProps) {
  const now = nowMs ?? (Date.parse(board.generated_at) || Date.now())
  return (
    <div className="flex flex-col gap-4 p-4">
      {board.notices.map((n) => (
        <p key={n} role="status" className="rounded-lg border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
          {n}
        </p>
      ))}
      <div className="grid gap-4 xl:grid-cols-2">
        {BOARD_SECTIONS.map((s) => (
          <BoardSection key={s.key} section={s.key} title={s.title} blurb={s.blurb} empty={s.empty} rows={board.sections[s.key]} total={board.totals[s.key]} now={now} />
        ))}
      </div>
      <p className="text-xs text-muted-foreground">
        Updated {new Date(board.generated_at).toLocaleTimeString()} · landed covers the last {board.recent_hours}h · a task with no update or comment for {board.active_hours}h counts as preparing, not in flight
      </p>
    </div>
  )
}

function BoardSection({ section, title, blurb, empty, rows, total, now }: { section: PlannerBoardSectionKey; title: string; blurb: string; empty: string; rows: PlannerBoardRow[]; total: number; now: number }) {
  const more = boardMore(rows.length, total)
  return (
    <section aria-label={title} className="flex min-w-0 flex-col rounded-xl border border-border">
      <header className="flex items-baseline gap-2 border-b border-border px-4 py-3">
        <h2 className="text-sm font-semibold uppercase tracking-[0.14em]">{title}</h2>
        <span className="text-sm text-muted-foreground">{rows.length}</span>
        {more && <span className="text-xs text-muted-foreground">({more})</span>}
        <span className="ml-auto hidden text-xs text-muted-foreground lg:inline">{blurb}</span>
      </header>
      {rows.length === 0 ? (
        <p className="px-4 py-5 text-sm text-muted-foreground">{empty}</p>
      ) : (
        <ul>
          {rows.map((r) => (
            <BoardRow key={`${r.source}/${r.id}`} section={section} row={r} now={now} />
          ))}
        </ul>
      )}
    </section>
  )
}

function BoardRow({ section, row, now }: { section: PlannerBoardSectionKey; row: PlannerBoardRow; now: number }) {
  const { word, tone } = boardWord(section, row)
  const t = statusTone(tone)
  const age = boardAge(row.updated, now)
  return (
    <li className="border-b border-border last:border-b-0">
      <Link to={boardRowRoute(row)} className="flex items-center gap-3 px-4 py-2.5 hover:bg-muted/50" aria-label={`${word} ${row.id}: ${row.title}`}>
        <span className={`inline-flex w-24 shrink-0 items-center gap-1.5 rounded border px-1.5 py-0.5 text-[10px] uppercase tracking-[0.12em] ${t.border} ${t.bg} ${t.text}`}>
          <span className={`h-1.5 w-1.5 rounded-full ${t.dot}`} />
          {word}
        </span>
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-sm">{row.title}</span>
          <span className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
            <span className="shrink-0 font-mono">{row.id}</span>
            {row.workstream && <span className="min-w-0 truncate rounded bg-muted px-1.5 py-px text-[11px]">{row.workstream}</span>}
          </span>
        </span>
        <span className="w-10 shrink-0 text-right text-xs text-muted-foreground">{age}</span>
      </Link>
    </li>
  )
}
