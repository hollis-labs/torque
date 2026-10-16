// Route helper for the PR workspace. Additive: one route, no existing path changes.
//   /pr/:host/:owner/:name/:number
import type { PrRef } from '../types'

export const PR_WORKSPACE_ROUTE = '/pr/:host/:owner/:name/:number'

/** App path for a PR's workspace. Optional `file` deep-links to a file in the diff. */
export function prWorkspacePath(ref: PrRef, opts: { file?: string } = {}): string {
  const { host, owner, name } = ref.repo
  const base = `/pr/${[host, owner, name].map(encodeURIComponent).join('/')}/${ref.number}`
  return opts.file ? `${base}?file=${encodeURIComponent(opts.file)}` : base
}

/** Build a PrRef from react-router params; null when malformed. */
export function prRefFromParams(p: { host?: string; owner?: string; name?: string; number?: string }): PrRef | null {
  const number = Number(p.number)
  if (!p.host || !p.owner || !p.name || !Number.isInteger(number) || number <= 0) return null
  return { repo: { host: p.host, owner: p.owner, name: p.name }, number }
}
