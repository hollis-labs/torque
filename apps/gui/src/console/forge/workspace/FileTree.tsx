import { useMemo, useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { buildFileTree, statusGlyph, type TreeNode } from './diff-utils'
import type { FileDiff } from '../types'

const GLYPH: Record<string, string> = { added: 'text-emerald-400', removed: 'text-red-400', renamed: 'text-sky-300', modified: 'text-amber-300' }

export interface FileTreeProps {
  files: FileDiff[]
  active?: string
  /** Comment-thread counts by path. */
  threads: Map<string, number>
  onSelect: (path: string) => void
}

export function FileTree({ files, active, threads, onSelect }: FileTreeProps) {
  const [filter, setFilter] = useState('')
  const [closed, setClosed] = useState<Set<string>>(new Set())
  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return q ? files.filter((f) => f.path.toLowerCase().includes(q)) : files
  }, [files, filter])
  const tree = useMemo(() => buildFileTree(shown), [shown])

  const toggle = (path: string) =>
    setClosed((c) => {
      const n = new Set(c)
      if (!n.delete(path)) n.add(path)
      return n
    })

  const render = (nodes: TreeNode[], depth: number) =>
    nodes.map((n) =>
      n.dir ? (
        <li key={n.path}>
          <button
            type="button"
            onClick={() => toggle(n.path)}
            aria-expanded={!closed.has(n.path)}
            style={{ paddingLeft: 8 + depth * 12 }}
            className="flex w-full items-center gap-1 py-1 pr-2 text-left font-mono text-[11px] text-zinc-500 hover:bg-zinc-900 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-amber-300"
          >
            {closed.has(n.path) ? <ChevronRight className="h-3 w-3 shrink-0" /> : <ChevronDown className="h-3 w-3 shrink-0" />}
            <span className="min-w-0 truncate">{n.name}</span>
          </button>
          {!closed.has(n.path) && <ul>{render(n.children, depth + 1)}</ul>}
        </li>
      ) : (
        <li key={n.path}>
          <button
            type="button"
            onClick={() => onSelect(n.path)}
            aria-current={active === n.path || undefined}
            title={n.path}
            style={{ paddingLeft: 8 + depth * 12 }}
            className={`flex w-full items-center gap-1.5 py-1 pr-2 text-left font-mono text-[11px] hover:bg-zinc-900 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-amber-300 ${active === n.path ? 'bg-zinc-900 text-zinc-100' : 'text-zinc-300'}`}
          >
            <span className={`w-3 shrink-0 text-center ${GLYPH[n.file!.status] ?? 'text-zinc-400'}`}>{statusGlyph(n.file!)}</span>
            <span className="min-w-0 flex-1 truncate">{n.name}</span>
            {(threads.get(n.path) ?? 0) > 0 && <span className="shrink-0 text-sky-300" aria-label={`${threads.get(n.path)} comment threads`}>{threads.get(n.path)}</span>}
          </button>
        </li>
      ),
    )

  return (
    <nav aria-label="Changed files" className="flex min-h-0 flex-col" data-testid="forge-file-tree">
      <div className="border-b border-zinc-800/80 p-2">
        <input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={`Filter ${files.length} files…`}
          aria-label="Filter files"
          className="h-7 w-full border border-zinc-800 bg-zinc-950 px-2 font-mono text-[11px] text-zinc-200 placeholder:text-zinc-600 focus-visible:border-amber-300 focus-visible:outline-none"
        />
      </div>
      <ul className="min-h-0 flex-1 overflow-auto py-1">{tree.length ? render(tree, 0) : <li className="px-3 py-2 text-[12px] text-zinc-500">No matching files.</li>}</ul>
    </nav>
  )
}
