// Host-neutral parsing for forge references. Canonical forms (stable cache keys):
//   repo    github:owner/name
//   pr      github:owner/name#123
//   commit  github:owner/name@<sha, lowercase, 7-40 hex>
// They extend repoKey()/prKey() from ../types, so a GitLab/Gitea ref is the
// same shape with a different host prefix.
import type { ForgeHost, PrRef, RepoRef } from '../types'

export type ForgeRefType = 'pr' | 'commit' | 'repo'

/** Web hosts we recognise in URLs, and the forge each one is. Extend via `registerForgeHost`. */
const HOSTS = new Map<string, { host: ForgeHost; pr: string; commit: string }>([
  ['github.com', { host: 'github', pr: 'pull', commit: 'commit' }],
  ['gitlab.com', { host: 'gitlab', pr: '-/merge_requests', commit: '-/commit' }],
  ['codeberg.org', { host: 'gitea', pr: 'pulls', commit: 'commit' }],
])

/** Teach the detector a self-hosted forge (e.g. a Gitea at git.example.com). */
export function registerForgeHost(domain: string, spec: { host: ForgeHost; prPath?: string; commitPath?: string }): () => void {
  const key = domain.toLowerCase()
  const prev = HOSTS.get(key)
  const next = {
    host: spec.host,
    pr: spec.prPath ?? (spec.host === 'gitlab' ? '-/merge_requests' : spec.host === 'gitea' ? 'pulls' : 'pull'),
    commit: spec.commitPath ?? (spec.host === 'gitlab' ? '-/commit' : 'commit'),
  }
  HOSTS.set(key, next)
  return () => {
    if (HOSTS.get(key) === next) {
      if (prev) HOSTS.set(key, prev)
      else HOSTS.delete(key)
    }
  }
}

export function hostDomains(): string[] {
  return [...HOSTS.keys()]
}

export const KNOWN_HOSTS: readonly string[] = ['github', 'gitlab', 'gitea']
const SHA = /^[0-9a-f]{7,40}$/

export function repoCanonical(r: RepoRef): string {
  return `${r.host}:${r.owner}/${r.name}`
}
export function prCanonical(p: PrRef): string {
  return `${repoCanonical(p.repo)}#${p.number}`
}
export function commitCanonical(r: RepoRef, sha: string): string {
  return `${repoCanonical(r)}@${sha.toLowerCase()}`
}

export function parseRepoCanonical(c: string): RepoRef | null {
  const m = /^([a-z][a-z0-9]*):([\w.-]+)\/([\w.-]+)$/.exec(c)
  return m ? { host: m[1], owner: m[2], name: m[3] } : null
}
export function parsePrCanonical(c: string): PrRef | null {
  const m = /^(.+)#(\d+)$/.exec(c)
  const repo = m ? parseRepoCanonical(m[1]) : null
  return m && repo ? { repo, number: Number(m[2]) } : null
}
export function parseCommitCanonical(c: string): { repo: RepoRef; sha: string } | null {
  const i = c.lastIndexOf('@')
  if (i < 0) return null
  const repo = parseRepoCanonical(c.slice(0, i))
  const sha = c.slice(i + 1)
  return repo && SHA.test(sha) ? { repo, sha } : null
}

/** Short display forms. A non-GitHub host is shown as a prefix so it is never mistaken for GitHub. */
export function repoLabel(r: RepoRef): string {
  return `${r.host === 'github' ? '' : `${r.host}:`}${r.owner}/${r.name}`
}
export const shortSha = (sha: string): string => sha.slice(0, 7)

/** Web URL for a ref on its forge (for "open on GitHub"). Uses the first registered domain of that host. */
export function repoUrl(r: RepoRef): string | undefined {
  for (const [domain, spec] of HOSTS) if (spec.host === r.host) return `https://${domain}/${r.owner}/${r.name}`
  return undefined
}
export function prUrl(p: PrRef): string | undefined {
  const base = repoUrl(p.repo)
  const spec = [...HOSTS.values()].find((s) => s.host === p.repo.host)
  return base && spec ? `${base}/${spec.pr}/${p.number}` : undefined
}
export function commitUrl(r: RepoRef, sha: string): string | undefined {
  const base = repoUrl(r)
  const spec = [...HOSTS.values()].find((s) => s.host === r.host)
  return base && spec ? `${base}/${spec.commit}/${sha}` : undefined
}
export function hostLabel(host: string): string {
  return host === 'github' ? 'GitHub' : host === 'gitlab' ? 'GitLab' : host === 'gitea' ? 'Gitea' : host
}

export interface ParsedUrl {
  type: ForgeRefType
  canonical: string
}

/** Parse a forge web URL (already trimmed of trailing punctuation). Unknown sub-pages (issues, blob...) return null. */
export function parseForgeUrl(url: string): ParsedUrl | null {
  const m = /^https?:\/\/(?:www\.)?([a-z0-9.-]+)\/([\w.-]+)\/([\w.-]+)((?:\/[\w.%~+-]+)*)\/?(?:[?#].*)?$/i.exec(url)
  if (!m) return null
  const spec = HOSTS.get(m[1].toLowerCase())
  if (!spec) return null
  const repo: RepoRef = { host: spec.host, owner: m[2], name: m[3].replace(/\.git$/, '') }
  const rest = m[4]
  if (rest === '') return { type: 'repo', canonical: repoCanonical(repo) }
  const pr = new RegExp(`^/${spec.pr}/(\\d+)(?:/.*)?$`).exec(rest)
  if (pr) return { type: 'pr', canonical: prCanonical({ repo, number: Number(pr[1]) }) }
  const co = new RegExp(`^/${spec.commit}/([0-9a-f]{7,40})$`, 'i').exec(rest)
  if (co) return { type: 'commit', canonical: commitCanonical(repo, co[1]) }
  return null
}
