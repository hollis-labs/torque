import type { KindResolver, ResolveOutcome } from '../../refs'
import type { ForgeClient } from '../client'
import { ForgeError, type PullRequest, type RepoRef } from '../types'
import { commitEntity, prEntity, repoEntity } from './entity'
import { parseCommitCanonical, parsePrCanonical, parseRepoCanonical } from './parse'

const MAX_PRS_SCANNED = 25

/** not_found (and malformed refs) become not-found; any other ForgeError fails the batch so the chip can retry. */
async function settle(canonicals: string[], one: (c: string) => Promise<ResolveOutcome>): Promise<Record<string, ResolveOutcome>> {
  const out: Record<string, ResolveOutcome> = {}
  await Promise.all(
    canonicals.map(async (c) => {
      try {
        out[c] = await one(c)
      } catch (e) {
        if (e instanceof ForgeError && (e.code === 'not_found' || e.code === 'invalid_args')) out[c] = { status: 'not-found' }
        else throw e
      }
    }),
  )
  return out
}

/**
 * The contract has no `getCommit`, so find the commit in the repo's PRs. This
 * is an interim lookup (see forge/BACKEND_NEEDS.md); it only covers commits
 * that belong to a PR.
 */
export async function findCommit(client: ForgeClient, repo: RepoRef, sha: string) {
  const page = await client.listPrs({ repo, state: 'all', limit: MAX_PRS_SCANNED })
  for (const pr of page.items as PullRequest[]) {
    const commits = await client.listCommits(pr.ref)
    const commit = commits.find((c) => c.sha.startsWith(sha) || sha.startsWith(c.sha))
    if (commit) return { commit, pr }
  }
  return undefined
}

export function createForgePrResolver(getClient: () => ForgeClient): KindResolver {
  return {
    resolveMany: (cs) =>
      settle(cs, async (c) => {
        const ref = parsePrCanonical(c)
        if (!ref) return { status: 'not-found' }
        return { status: 'found', entity: prEntity(await getClient().getPr(ref)) }
      }),
  }
}

export function createForgeCommitResolver(getClient: () => ForgeClient): KindResolver {
  return {
    resolveMany: (cs) =>
      settle(cs, async (c) => {
        const p = parseCommitCanonical(c)
        if (!p) return { status: 'not-found' }
        const hit = await findCommit(getClient(), p.repo, p.sha)
        return hit ? { status: 'found', entity: commitEntity({ repo: p.repo, ...hit }) } : { status: 'not-found' }
      }),
  }
}

export function createForgeRepoResolver(getClient: () => ForgeClient): KindResolver {
  return {
    resolveMany: (cs) =>
      settle(cs, async (c) => {
        const r = parseRepoCanonical(c)
        if (!r) return { status: 'not-found' }
        return { status: 'found', entity: repoEntity(await getClient().getRepo(r)) }
      }),
  }
}

/** Resolver map to merge into AutolinkProvider's `resolvers`. Reads happen through the installed ForgeClient. */
export function createForgeResolvers(getClient: () => ForgeClient): Record<string, KindResolver> {
  return {
    'forge.pr': createForgePrResolver(getClient),
    'forge.commit': createForgeCommitResolver(getClient),
    'forge.repo': createForgeRepoResolver(getClient),
  }
}
