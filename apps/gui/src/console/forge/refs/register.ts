import { getRefRegistry, type KindResolver, type RefRegistry } from '../../refs'
import { registerRefActions, registerRefDetail } from '../../autolink/extensions'
import type { ForgeClient } from '../client'
import { ForgeCommitDetail, ForgePrDetail, ForgeRepoDetail } from './Detail'
import { getForgeClient, setForgeClient } from './client'
import { createForgeKinds, setKnownRepos } from './kinds'
import {
  commitUrl, hostLabel, parseCommitCanonical, parsePrCanonical, parseRepoCanonical, prUrl, repoUrl,
} from './parse'
import { createForgeResolvers } from './resolvers'
import type { RepoRef } from '../types'

export interface RegisterForgeRefsOptions {
  registry?: RefRegistry
  /** Install this client for resolvers and drawers. Omit to keep the current/default (mock) client. */
  client?: ForgeClient
  /** Replace the repos whose bare `owner/name` is linked. */
  knownRepos?: readonly RepoRef[]
}

/**
 * Register forge.pr / forge.commit / forge.repo with the F2 registry and the
 * R1 extension points (icons, quick actions, drawer bodies). Idempotent
 * (replaces earlier registrations). Returns the resolver map to merge into
 * AutolinkProvider's `resolvers`, and an `unregister` that undoes everything.
 */
export function registerForgeRefs(opts: RegisterForgeRefsOptions = {}): {
  resolvers: Record<string, KindResolver>
  unregister: () => void
} {
  const registry = opts.registry ?? getRefRegistry()
  const undo: (() => void)[] = []
  if (opts.client) undo.push(setForgeClient(opts.client))
  if (opts.knownRepos) undo.push(setKnownRepos(opts.knownRepos))

  for (const kind of createForgeKinds()) undo.push(registry.register(kind, { replace: true }))

  undo.push(registerRefDetail('forge.pr', ForgePrDetail), registerRefDetail('forge.commit', ForgeCommitDetail), registerRefDetail('forge.repo', ForgeRepoDetail))

  // Links out to the forge. Host-neutral: the label follows the ref's host.
  undo.push(
    registerRefActions('forge.pr', (ref) => {
      const p = parsePrCanonical(ref.canonical)
      const href = p && prUrl(p)
      return p && href ? [{ id: 'forge', label: `Open on ${hostLabel(p.repo.host)}`, type: 'open', target: { type: 'external', href } }] : []
    }),
    registerRefActions('forge.commit', (ref) => {
      const c = parseCommitCanonical(ref.canonical)
      const href = c && commitUrl(c.repo, c.sha)
      return c
        ? [
            ...(href ? [{ id: 'forge', label: `Open on ${hostLabel(c.repo.host)}`, type: 'open' as const, target: { type: 'external' as const, href } }] : []),
            { id: 'copy-sha', label: 'Copy SHA', type: 'copy' as const, text: c.sha },
          ]
        : []
    }),
    registerRefActions('forge.repo', (ref) => {
      const r = parseRepoCanonical(ref.canonical)
      const href = r && repoUrl(r)
      return r && href ? [{ id: 'forge', label: `Open on ${hostLabel(r.host)}`, type: 'open', target: { type: 'external', href } }] : []
    }),
  )

  return { resolvers: createForgeResolvers(getForgeClient), unregister: () => undo.reverse().forEach((u) => u()) }
}
