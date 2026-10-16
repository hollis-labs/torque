// Forge references (G2): PR / commit / repo kinds for the autolink registry.
//
//   const { resolvers } = registerForgeRefs()        // once at startup
//   <AutolinkProvider resolvers={{ ...live, ...resolvers }}>
//
// Reads go through the installed ForgeClient (mock by default, HttpForgeClient
// via `?forge=http`). Read-only: writes belong to G3/G4 behind ActionBus +
// IdentityConfirm.
export { registerForgeRefs, type RegisterForgeRefsOptions } from './register'
export { getForgeClient, setForgeClient } from './client'
export { createForgeKinds, getKnownRepos, setKnownRepos } from './kinds'
export { createForgeResolvers, createForgePrResolver, createForgeCommitResolver, createForgeRepoResolver, findCommit } from './resolvers'
export { prEntity, commitEntity, repoEntity, summarizeChecks, checksText, reviewText, sizeText, prStatus } from './entity'
export type { ChecksSummary, CommitContext } from './entity'
export { ForgePrDetail, ForgeCommitDetail, ForgeRepoDetail } from './Detail'
export {
  commitCanonical, commitUrl, hostLabel, parseCommitCanonical, parseForgeUrl, parsePrCanonical, parseRepoCanonical,
  prCanonical, prUrl, registerForgeHost, repoCanonical, repoLabel, repoUrl, shortSha,
} from './parse'
