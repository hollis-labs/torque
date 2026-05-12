import type { Artifact, HITLWorkflowPreset, Task } from './types'

const URL_PATTERN = /https?:\/\/[^\s"'<>]+/gi

export function buildHITLCheckpointPayload(
  preset: HITLWorkflowPreset,
  task: Task,
  artifacts: Artifact[] = [],
): Record<string, unknown> {
  const context = buildTaskContext(task, artifacts)
  const summary = task.description.trim() || task.title
  const prUrl = findLikelyPullRequestUrl(task, artifacts)

  if (preset === 'pr_review') {
    return {
      pr_url: prUrl ?? '',
      title: task.title,
      summary,
      task_id: task.id,
      context,
    }
  }

  if (preset === 'approval') {
    return {
      title: `Approval requested: ${task.title}`,
      prompt: `Please review and approve task ${task.id}.`,
      context,
      options: ['approved', 'rejected', 'needs_info'],
    }
  }

  return {
    subject: `Task update: ${task.title}`,
    message: `Please review task ${task.id}: ${task.title}`,
    severity: 'info',
    context,
  }
}

export function findLikelyPullRequestUrl(
  task: Task,
  artifacts: Artifact[] = [],
): string | null {
  const preferred = artifacts.find((artifact) => {
    return artifact.type === 'pr-link' && artifact.url.trim() !== ''
  })
  if (preferred) return preferred.url.trim()

  const candidates: string[] = []
  if (task.source_ref) candidates.push(task.source_ref)
  candidates.push(task.description)
  for (const artifact of artifacts) {
    candidates.push(artifact.url, artifact.content, artifact.file_path)
    for (const value of Object.values(artifact.metadata ?? {})) {
      if (typeof value === 'string') candidates.push(value)
    }
  }

  for (const candidate of candidates) {
    for (const url of extractUrls(candidate)) {
      if (looksLikePullRequestUrl(url)) return url
    }
  }
  return null
}

function buildTaskContext(task: Task, artifacts: Artifact[]): Record<string, unknown> {
  const prUrls = [
    ...new Set(
      artifacts
        .flatMap((artifact) => [
          artifact.url,
          artifact.content,
          artifact.file_path,
          ...Object.values(artifact.metadata ?? {}).filter(
            (value): value is string => typeof value === 'string',
          ),
        ])
        .flatMap(extractUrls)
        .filter(looksLikePullRequestUrl),
    ),
  ]

  return {
    task_id: task.id,
    title: task.title,
    status: task.status,
    description: task.description.trim(),
    blocked_reason: task.blocked_reason,
    source_type: task.source_type,
    source_ref: task.source_ref,
    working_dir: task.working_dir,
    tags: task.tags.map((tag) => tag.slug),
    files: task.files,
    deliverables: task.deliverables,
    artifact_urls: artifacts.map((artifact) => artifact.url).filter(Boolean),
    pr_urls: prUrls,
  }
}

function extractUrls(value: string): string[] {
  return Array.from(value.matchAll(URL_PATTERN), (match) =>
    match[0].replace(/[),.;\]]+$/, ''),
  )
}

function looksLikePullRequestUrl(url: string): boolean {
  return /\/(pull|pulls|merge_requests)\/\d+/i.test(url)
}
