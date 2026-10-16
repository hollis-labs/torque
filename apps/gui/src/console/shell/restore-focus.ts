import { useEffect } from 'react'

/**
 * While `active`, remember the element that had focus when it became true and give
 * focus back when it turns false or the component unmounts, if focus was lost with the
 * overlay (activeElement is body or detached). `fallback` is tried when the remembered
 * element is gone (e.g. a dismissed list row). Declare this hook BEFORE any effect that
 * moves focus into the overlay.
 */
export function useRestoreFocus(active: boolean, fallback?: string) {
  useEffect(() => {
    if (!active) return
    const prev = document.activeElement as HTMLElement | null
    return () => {
      const cur = document.activeElement
      if (cur && cur !== document.body && cur.isConnected) return
      const target = prev?.isConnected && prev !== document.body ? prev : fallback ? document.querySelector<HTMLElement>(fallback) : null
      target?.focus()
    }
  }, [active, fallback])
}
