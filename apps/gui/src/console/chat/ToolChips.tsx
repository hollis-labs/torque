import type { ToolChip } from './types'

const TONE = { running: 'border-amber-400/40 text-amber-300', ok: 'border-zinc-700 text-zinc-300', error: 'border-red-400/50 text-red-300' } as const
const MARK = { running: '...', ok: 'ok', error: 'err' } as const

export function ToolChips({ tools }: { tools: ToolChip[] }) {
  if (tools.length === 0) return null
  return (
    <ul aria-label="Tool calls" className="mt-1.5 flex flex-wrap gap-1.5">
      {tools.map((t) => (
        <li key={t.id} data-status={t.status} title={t.detail} className={`inline-flex items-center gap-1.5 border px-1.5 py-0.5 font-mono text-[10px] ${TONE[t.status]}`}>
          <span className={t.status === 'running' ? 'animate-pulse' : ''}>{MARK[t.status]}</span>
          <span>{t.name}</span>
          {t.detail && <span className="max-w-40 truncate text-zinc-500">{t.detail}</span>}
        </li>
      ))}
    </ul>
  )
}
