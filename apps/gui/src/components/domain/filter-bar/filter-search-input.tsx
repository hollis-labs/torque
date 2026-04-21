import { useEffect, useRef, useState } from 'react'
import { Search } from 'lucide-react'

interface FilterSearchInputProps {
  value: string
  onChange: (next: string) => void
  placeholder?: string
}

const DEBOUNCE_MS = 250

function isEditableTarget(el: Element | null): boolean {
  if (!el) return false
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) return true
  if (el instanceof HTMLElement && el.isContentEditable) return true
  return false
}

export function FilterSearchInput({
  value,
  onChange,
  placeholder = 'Search tasks by title or description…',
}: FilterSearchInputProps) {
  const [local, setLocal] = useState(value)
  const inputRef = useRef<HTMLInputElement | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lastEmittedRef = useRef(value)

  // Keep local value in sync when the prop changes externally (e.g. Clear).
  useEffect(() => {
    setLocal(value)
    lastEmittedRef.current = value
  }, [value])

  // Debounced emit. Only fires when local !== last-emitted to avoid churn.
  useEffect(() => {
    if (local === lastEmittedRef.current) return
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      lastEmittedRef.current = local
      onChange(local)
    }, DEBOUNCE_MS)
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [local, onChange])

  // `/` focuses the input when no other editable element holds focus.
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (e.key !== '/') return
      if (isEditableTarget(document.activeElement)) return
      e.preventDefault()
      inputRef.current?.focus()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  return (
    <div className="flex min-w-[240px] flex-1 items-center gap-2 rounded border border-zinc-800 bg-zinc-900/50 px-2.5 py-1 focus-within:border-zinc-600">
      <Search className="h-3.5 w-3.5 text-zinc-500" />
      <input
        ref={inputRef}
        type="search"
        aria-label="Search tasks"
        placeholder={placeholder}
        value={local}
        onChange={(e) => setLocal(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            setLocal('')
            inputRef.current?.blur()
          }
        }}
        className="flex-1 bg-transparent text-xs text-zinc-100 placeholder:text-zinc-600 focus:outline-none"
      />
      <kbd className="rounded border border-zinc-800 bg-zinc-950 px-1 text-[9px] uppercase tracking-wider text-zinc-500">/</kbd>
    </div>
  )
}
