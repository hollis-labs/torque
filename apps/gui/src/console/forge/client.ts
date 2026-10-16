import type {
  AddCommentInput,
  AssignInput,
  Commit,
  FileDiff,
  ForgeComment,
  ListPrsQuery,
  Page,
  PendingPrSummary,
  PrRef,
  PullRequest,
  Repo,
  RepoRef,
  Review,
  ReviewInput,
} from './types'

/**
 * Forge-neutral client the Console codes to. The mock implements it today; G1
 * adds a typed HTTP client behind the same interface. No network or secrets
 * live in the GUI: any real implementation talks to the Torque/Cerberus backend.
 */
export interface ForgeClient {
  /** effect: read */
  getRepo(repo: RepoRef): Promise<Repo>
  /** effect: read */
  listPrs(query?: ListPrsQuery): Promise<Page<PullRequest>>
  /** effect: read */
  getPr(ref: PrRef): Promise<PullRequest>
  /** effect: read */
  getDiff(ref: PrRef): Promise<FileDiff[]>
  /** effect: read */
  listCommits(ref: PrRef): Promise<Commit[]>
  /** effect: read */
  listComments(ref: PrRef): Promise<ForgeComment[]>
  /** effect: write */
  addComment(ref: PrRef, input: AddCommentInput): Promise<ForgeComment>
  /** effect: write, ack */
  reviewPr(ref: PrRef, input: ReviewInput): Promise<Review>
  /** effect: write */
  assign(ref: PrRef, input: AssignInput): Promise<PullRequest>
  /** effect: read */
  listPendingPrs(): Promise<PendingPrSummary[]>
}
