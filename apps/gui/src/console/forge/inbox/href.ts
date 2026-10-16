import { prKey, type PrRef } from '../types'
import { prWorkspacePath } from '../workspace/route'

/** Route of the PR workspace for one PR (delegates to the G3 workspace route helper). */
export function prWorkspaceHref(ref: PrRef): string {
  return prWorkspacePath(ref)
}

/** Where the inbox itself lives (used by cards/alerts that link to triage). */
export const PR_INBOX_PATH = '/prs/inbox'

/** Stable text id of a PR, e.g. "github:hollis-labs/torque#265". Re-exported for convenience. */
export const prId = prKey
