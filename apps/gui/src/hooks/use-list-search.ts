import { useEffect, useState } from 'react'

/** Delay server search while preserving cancellation/query reset in usePagedList. */
export function useListSearch(input: string) {
  const [search, setSearch] = useState(input.trim())
  useEffect(() => {
    const timer = setTimeout(() => setSearch(input.trim()), 250)
    return () => clearTimeout(timer)
  }, [input])
  return search
}
