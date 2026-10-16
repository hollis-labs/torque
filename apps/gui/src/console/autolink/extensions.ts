// Extension points for the autolink renderer. Feature modules (G2: forge
// PR/commit/repo; R2: app-wide rollout) call these at startup, next to their
// `registry.register(kind)`, so a new kind gets an icon, extra quick actions
// and a richer drawer body without touching R1 code. All registries are
// module-level and return an unregister function.
import type { ComponentType } from 'react'
import {
  Bot, BookOpen, Brain, ClipboardList, FileText, FolderKanban, GitBranch, GitCommitHorizontal, GitPullRequest,
  Layers, Link as LinkIcon, ListChecks, Milestone, Workflow, ExternalLink, type LucideIcon,
} from 'lucide-react'
import type { OpenTarget, PreviewCard, RefIdentity } from '../refs'

// ---------- icons ----------

const icons = new Map<string, LucideIcon>(Object.entries({
  task: ListChecks, epic: Layers, project: FolderKanban, sprint: Milestone, plan: ClipboardList,
  memory: Brain, agent: Bot, workstream: Workflow, file: FileText, url: ExternalLink, link: LinkIcon,
  // Pre-registered for G2 so forge kinds can name them without importing lucide.
  pr: GitPullRequest, commit: GitCommitHorizontal, repo: BookOpen, branch: GitBranch,
}))

/** Map an icon token (`RefKind.icon`) to a glyph. Unknown tokens fall back to a link icon. */
export function registerRefIcon(token: string, icon: LucideIcon): () => void {
  icons.set(token, icon)
  return () => {
    if (icons.get(token) === icon) icons.delete(token)
  }
}
export function refIcon(token: string): LucideIcon {
  return icons.get(token) ?? LinkIcon
}

// ---------- quick actions ----------

/**
 * Phase 1 is read-only: actions navigate, open externally or copy. A mutating
 * action kind (approve, assign) is a later addition and must go through the
 * console ActionBus and IdentityConfirm, not through this type.
 */
export type RefAction =
  | { id: string; label: string; type: 'open'; target: Extract<OpenTarget, { type: 'route' | 'external' }> }
  | { id: string; label: string; type: 'copy'; text: string }

export type RefActionProvider = (ref: RefIdentity, card: PreviewCard) => RefAction[]

const actionProviders = new Map<string, RefActionProvider[]>()

/** Extra quick actions for a kind, appended after the defaults (open, copy id). */
export function registerRefActions(kind: string, provider: RefActionProvider): () => void {
  actionProviders.set(kind, [...(actionProviders.get(kind) ?? []), provider])
  return () => {
    const rest = (actionProviders.get(kind) ?? []).filter((p) => p !== provider)
    if (rest.length) actionProviders.set(kind, rest)
    else actionProviders.delete(kind)
  }
}

export function refActions(ref: RefIdentity, card: PreviewCard): RefAction[] {
  const out: RefAction[] = []
  if (card.open.type === 'route') out.push({ id: 'open', label: 'Open page', type: 'open', target: card.open })
  else if (card.open.type === 'external') out.push({ id: 'open', label: 'Open link', type: 'open', target: card.open })
  out.push({ id: 'copy', label: card.open.type === 'copy' ? 'Copy path' : 'Copy ID', type: 'copy', text: card.open.type === 'copy' ? card.open.text : ref.canonical })
  for (const p of actionProviders.get(ref.kind) ?? []) {
    try {
      out.push(...p(ref, card))
    } catch {
      // A misbehaving provider must not break the card.
    }
  }
  return out
}

// ---------- drawer body ----------

export interface RefDetailProps {
  refId: RefIdentity
  card: PreviewCard
}

const detailRenderers = new Map<string, ComponentType<RefDetailProps>>()

/**
 * Replace the drawer body for a kind. The renderer is shown below the standard
 * header (icon, title, status) and above the standard actions; it receives the
 * resolved card and may fetch richer data itself.
 */
export function registerRefDetail(kind: string, component: ComponentType<RefDetailProps>): () => void {
  detailRenderers.set(kind, component)
  return () => {
    if (detailRenderers.get(kind) === component) detailRenderers.delete(kind)
  }
}
export function refDetail(kind: string): ComponentType<RefDetailProps> | undefined {
  return detailRenderers.get(kind)
}
