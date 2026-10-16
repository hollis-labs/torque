import { isWordChar, matches, trimTrailing } from '../../refs/scan-utils'
import type { RefHit, RefKind } from '../../refs'
import type { RepoRef } from '../types'
import {
  KNOWN_HOSTS, commitCanonical, hostDomains, parseCommitCanonical, parseForgeUrl, parsePrCanonical, parseRepoCanonical,
  prCanonical, repoCanonical, repoLabel, shortSha, type ForgeRefType,
} from './parse'

// One priority per kind (the registry has no per-hit priority). PR and commit
// sit above the generic url kind (100) so a forge link becomes a chip rather
// than a bare link; repo is just below them so `owner/name#1` stays a PR.
export const FORGE_PR_PRIORITY = 110
export const FORGE_COMMIT_PRIORITY = 110
export const FORGE_REPO_PRIORITY = 105

/** Repos whose bare `owner/name` is unambiguous enough to link. Mutable at runtime. */
const knownRepos = new Map<string, RepoRef>()
function addKnown(r: RepoRef): void {
  knownRepos.set(repoCanonical(r).toLowerCase(), r)
}
for (const r of [
  { host: 'github', owner: 'hollis-labs', name: 'torque' },
  { host: 'github', owner: 'hollis-labs', name: 'go-runtime' },
]) addKnown(r)

/** Replace the known-repo list (e.g. from settings or the repos agents work in). Returns a restore function. */
export function setKnownRepos(repos: readonly RepoRef[]): () => void {
  const prev = [...knownRepos.values()]
  knownRepos.clear()
  repos.forEach(addKnown)
  return () => {
    knownRepos.clear()
    prev.forEach(addKnown)
  }
}
export function getKnownRepos(): RepoRef[] {
  return [...knownRepos.values()]
}

// Words that look like owners but make `a/b#1` read as something else (a path or "and/or").
const FILE_EXT = /\.(?:ts|tsx|js|jsx|mjs|cjs|go|py|rs|md|json|ya?ml|toml|css|html|sh|txt|sql|lock|log|svg|png)$/i
const HOST_PREFIX = `(?:(${KNOWN_HOSTS.join('|')}):)?`
const REPO_PART = '([A-Za-z0-9][A-Za-z0-9-]*)\\/([A-Za-z0-9][\\w.-]*)'

/** A shorthand must start at a boundary: not inside a word, path, URL or longer token. */
function boundedBefore(text: string, start: number): boolean {
  const b = text[start - 1]
  return !(isWordChar(b) || b === '/' || b === '\\' || b === '.' || b === '-' || b === ':' || b === '@' || b === '#' || b === '~')
}
function boundedAfter(text: string, end: number): boolean {
  const a = text[end]
  return !(isWordChar(a) || a === '/' || a === '\\' || a === '-')
}
const validName = (n: string): boolean => !n.endsWith('.') && !FILE_EXT.test(n)

function urlHits(text: string, type: ForgeRefType): RefHit[] {
  const domains = hostDomains().map((d) => d.replace(/\./g, '\\.')).join('|')
  const re = new RegExp(`https?:\\/\\/(?:www\\.)?(?:${domains})\\/[^\\s<>"'\`]+`, 'gi')
  const out: RefHit[] = []
  for (const m of matches(re, text)) {
    const end = trimTrailing(text, m.index, m.index + m[0].length)
    const parsed = parseForgeUrl(text.slice(m.index, end))
    if (parsed?.type === type) out.push({ start: m.index, end, canonical: parsed.canonical })
  }
  return out
}

function prShorthand(text: string): RefHit[] {
  const re = new RegExp(`${HOST_PREFIX}${REPO_PART}#(\\d{1,7})`, 'g')
  const out: RefHit[] = []
  for (const m of matches(re, text)) {
    const end = m.index + m[0].length
    if (!boundedBefore(text, m.index) || !boundedAfter(text, end) || !validName(m[3])) continue
    out.push({ start: m.index, end, canonical: prCanonical({ repo: { host: m[1] ?? 'github', owner: m[2], name: m[3] }, number: Number(m[4]) }) })
  }
  return out
}

function commitShorthand(text: string): RefHit[] {
  const re = new RegExp(`${HOST_PREFIX}${REPO_PART}@([0-9a-fA-F]{7,40})`, 'g')
  const out: RefHit[] = []
  for (const m of matches(re, text)) {
    const end = m.index + m[0].length
    if (!boundedBefore(text, m.index) || !boundedAfter(text, end) || !validName(m[3])) continue
    // A pure-digit "sha" is a number (a version, a count), not a commit.
    if (/^\d+$/.test(m[4])) continue
    out.push({ start: m.index, end, canonical: commitCanonical({ host: m[1] ?? 'github', owner: m[2], name: m[3] }, m[4]) })
  }
  return out
}

function repoShorthand(text: string): RefHit[] {
  if (knownRepos.size === 0) return []
  const re = new RegExp(`${HOST_PREFIX}${REPO_PART}`, 'g')
  const out: RefHit[] = []
  for (const m of matches(re, text)) {
    const end = trimTrailing(text, m.index, m.index + m[0].length)
    const candidate = text.slice(m.index, end)
    const parsed = parseRepoCanonical(candidate.includes(':') ? candidate : `github:${candidate}`)
    if (!parsed || !knownRepos.has(repoCanonical(parsed).toLowerCase())) continue
    if (!boundedBefore(text, m.index) || !boundedAfter(text, end)) continue
    out.push({ start: m.index, end, canonical: repoCanonical(knownRepos.get(repoCanonical(parsed).toLowerCase())!) })
  }
  return out
}

export function createForgeKinds(): RefKind[] {
  return [
    {
      id: 'forge.pr',
      label: 'Pull request',
      icon: 'pr',
      priority: FORGE_PR_PRIORITY,
      scan: (text) => [...urlHits(text, 'pr'), ...prShorthand(text)],
      chipLabel(ref) {
        const p = parsePrCanonical(ref.canonical)
        return p ? `${repoLabel(p.repo)}#${p.number}` : ref.canonical
      },
      open: () => ({ type: 'none' }),
    },
    {
      id: 'forge.commit',
      label: 'Commit',
      icon: 'commit',
      priority: FORGE_COMMIT_PRIORITY,
      scan: (text) => [...urlHits(text, 'commit'), ...commitShorthand(text)],
      chipLabel(ref) {
        const c = parseCommitCanonical(ref.canonical)
        return c ? `${repoLabel(c.repo)}@${shortSha(c.sha)}` : ref.canonical
      },
      open: () => ({ type: 'none' }),
    },
    {
      id: 'forge.repo',
      label: 'Repository',
      icon: 'repo',
      priority: FORGE_REPO_PRIORITY,
      scan: (text) => [...urlHits(text, 'repo'), ...repoShorthand(text)],
      chipLabel(ref) {
        const r = parseRepoCanonical(ref.canonical)
        return r ? repoLabel(r) : ref.canonical
      },
      open: () => ({ type: 'none' }),
    },
  ]
}
