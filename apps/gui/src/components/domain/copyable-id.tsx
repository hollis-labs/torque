import { useState } from 'react'
import { Check, Copy } from 'lucide-react'

interface CopyableIdProps {
  id: string
}

export function CopyableId({ id }: CopyableIdProps) {
  const [copied, setCopied] = useState(false)

  function handleCopy(e: React.MouseEvent) {
    e.stopPropagation()
    navigator.clipboard.writeText(id)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <button
      type="button"
      onClick={handleCopy}
      className="inline-flex items-center gap-1 text-[10px] font-mono text-zinc-500 hover:text-zinc-300 transition-colors"
      title="Copy ID"
      aria-label={copied ? `Copied ${id}` : `Copy ${id}`}
    >
      {id}
      {copied ? (
        <Check className="h-2.5 w-2.5 text-emerald-400" />
      ) : (
        <Copy className="h-2.5 w-2.5" />
      )}
    </button>
  )
}
