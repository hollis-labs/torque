import { useState } from 'react'
import { FolderOpen, ExternalLink, Trash2 } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@hollis-labs/sysop-ui'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import type { Artifact } from '@/lib/types'

const IMAGE_EXTENSIONS = /\.(png|jpe?g|gif|webp|bmp|svg|avif|apng|heic)$/i

function isImageArtifact(a: Artifact): boolean {
  if (a.type === 'screenshot' || a.type === 'image') return true
  if (a.file_path && IMAGE_EXTENSIONS.test(a.file_path)) return true
  return false
}

type Origin = 'agent' | 'user' | 'system'

const ORIGIN_BADGE: Record<Origin, string> = {
  agent: 'bg-violet-950/50 text-violet-300 ring-violet-800/50',
  user: 'bg-emerald-950/50 text-emerald-300 ring-emerald-800/50',
  system: 'bg-zinc-900 text-zinc-400 ring-zinc-700',
}

function readOrigin(metadata: Artifact['metadata']): Origin | null {
  const raw = metadata?.origin
  if (raw === 'agent' || raw === 'user' || raw === 'system') return raw
  return null
}

interface ArtifactCardProps {
  artifact: Artifact
  onDelete?: (id: number) => Promise<void> | void
}

export function ArtifactCard({ artifact, onDelete }: ArtifactCardProps) {
  const api = useApi()
  const [lightboxOpen, setLightboxOpen] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)

  const hasFile = artifact.file_path !== ''
  const contentUrl = hasFile ? api.artifactContentUrl(artifact.id) : ''
  const showImage = hasFile && isImageArtifact(artifact)
  const origin = readOrigin(artifact.metadata)
  const metadataJson = artifact.metadata
    ? JSON.stringify(artifact.metadata, null, 2)
    : ''

  async function handleConfirmDelete() {
    if (!onDelete) return
    setDeleting(true)
    try {
      await onDelete(artifact.id)
      setConfirmOpen(false)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="group relative rounded-md border border-zinc-800/50 p-3">
      <div className="flex items-center gap-2 mb-1">
        <FolderOpen className="h-3 w-3 text-zinc-500" aria-hidden />
        <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
          {artifact.type}
        </span>
        {origin && (
          <span
            className={`rounded-full px-1.5 py-0.5 text-[9px] uppercase tracking-[.14em] ring-1 ${ORIGIN_BADGE[origin]}`}
          >
            {origin}
          </span>
        )}
        {onDelete && (
          <button
            type="button"
            onClick={() => setConfirmOpen(true)}
            aria-label={`Delete artifact ${artifact.id}`}
            className="ml-auto rounded p-1 text-zinc-600 opacity-0 transition-opacity hover:bg-zinc-900 hover:text-red-400 focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-red-500/50 group-hover:opacity-100"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        )}
      </div>

      {hasFile && (
        <a
          href={contentUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="text-[11px] font-mono text-blue-400 hover:text-blue-300 break-all"
        >
          {artifact.file_path}
        </a>
      )}

      {showImage && (
        <button
          type="button"
          onClick={() => setLightboxOpen(true)}
          className="mt-2 block rounded-md border border-zinc-800/60 bg-zinc-900/40 p-1 hover:border-zinc-700 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-zinc-500"
          aria-label={`Open ${artifact.file_path} in lightbox`}
        >
          <img
            src={contentUrl}
            alt={artifact.file_path}
            className="max-h-48 w-auto rounded-sm object-contain"
            loading="lazy"
          />
        </button>
      )}

      {artifact.url && (
        <a
          href={artifact.url}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-1 inline-flex items-center gap-1 text-[11px] text-blue-400 hover:text-blue-300 break-all"
        >
          <ExternalLink className="h-3 w-3" aria-hidden />
          <span>{artifact.url}</span>
        </a>
      )}

      {artifact.content && (
        <pre className="mt-2 text-[11px] bg-zinc-900/60 rounded p-2 overflow-auto max-h-40 whitespace-pre-wrap text-zinc-300">
          {artifact.content}
        </pre>
      )}

      {metadataJson && (
        <details className="group mt-2 rounded bg-zinc-900/40 open:bg-zinc-900/60">
          <summary className="cursor-pointer select-none px-2 py-1 text-[10px] uppercase tracking-[.18em] text-zinc-500 hover:text-zinc-300 list-none [&::-webkit-details-marker]:hidden">
            <span className="inline-block transition-transform group-open:rotate-90">▸</span>
            <span className="ml-2">Metadata</span>
          </summary>
          <pre className="mx-2 mb-2 mt-1 text-[11px] bg-zinc-950/80 rounded p-2 overflow-auto max-h-48 whitespace-pre-wrap text-zinc-300">
            {metadataJson}
          </pre>
        </details>
      )}

      {showImage && (
        <Dialog open={lightboxOpen} onOpenChange={setLightboxOpen}>
          <DialogContent
            className="flex w-auto max-w-none sm:max-w-none bg-zinc-950 p-2 ring-zinc-700"
            showCloseButton
          >
            <DialogTitle className="sr-only">{artifact.file_path}</DialogTitle>
            <img
              src={contentUrl}
              alt={artifact.file_path}
              className="max-w-[92vw] max-h-[88vh] w-auto h-auto rounded-md object-contain"
            />
          </DialogContent>
        </Dialog>
      )}

      {onDelete && (
        <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete this artifact?</AlertDialogTitle>
              <AlertDialogDescription>
                The database row will be removed. The underlying file on disk
                is not touched.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={deleting}>Cancel</AlertDialogCancel>
              <AlertDialogAction
                variant="destructive"
                onClick={(e) => {
                  e.preventDefault()
                  void handleConfirmDelete()
                }}
                disabled={deleting}
              >
                {deleting ? 'Deleting…' : 'Delete'}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
    </div>
  )
}
