import { useEffect } from 'react'

interface ArrowNavOptions {
  enabled?: boolean
  onPrev: () => void
  onNext: () => void
}

/**
 * Listens for ArrowLeft / ArrowRight on window and calls the supplied
 * handlers. Skips events targeting editable fields (input / textarea /
 * select / contentEditable) and any modifier-key combinations so native
 * cursor movement and browser shortcuts continue to work.
 */
export function useArrowNav({ enabled = true, onPrev, onNext }: ArrowNavOptions): void {
  useEffect(() => {
    if (!enabled) return
    const handler = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return
      const t = e.target
      if (
        t instanceof HTMLInputElement ||
        t instanceof HTMLTextAreaElement ||
        t instanceof HTMLSelectElement ||
        (t instanceof HTMLElement && t.isContentEditable)
      ) {
        return
      }
      if (e.key === 'ArrowLeft') {
        e.preventDefault()
        onPrev()
      } else if (e.key === 'ArrowRight') {
        e.preventDefault()
        onNext()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [enabled, onPrev, onNext])
}
