import { useState } from 'react'
import { FolderOpen, ExternalLink } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useApi } from '@/hooks/use-api'
import type { Artifact } from '@/lib/types'

const IMAGE_EXTENSIONS = /\.(png|jpe?g|gif|webp|bmp|svg|avif|apng|heic)$/i

function isImageArtifact(a: Artifact): boolean {
  if (a.type === 'screenshot' || a.type === 'image') return true
  if (a.file_path && IMAGE_EXTENSIONS.test(a.file_path)) return true
  return false
}

interface ArtifactCardProps {
  artifact: Artifact
}

export function ArtifactCard({ artifact }: ArtifactCardProps) {
  const api = useApi()
  const [lightboxOpen, setLightboxOpen] = useState(false)

  const hasFile = artifact.file_path !== ''
  const contentUrl = hasFile ? api.artifactContentUrl(artifact.id) : ''
  const showImage = hasFile && isImageArtifact(artifact)

  return (
    <div className="rounded-md border border-zinc-800/50 p-3">
      <div className="flex items-center gap-2 mb-1">
        <FolderOpen className="h-3 w-3 text-zinc-500" aria-hidden />
        <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
          {artifact.type}
        </span>
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

      {showImage && (
        <Dialog open={lightboxOpen} onOpenChange={setLightboxOpen}>
          <DialogContent
            className="bg-zinc-950 ring-zinc-700 max-w-none sm:max-w-none w-auto p-2"
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
    </div>
  )
}
