import type { Tag } from '@/lib/types'
import { TAG_COLOR_CLASSES } from '@/lib/constants'

interface TagChipProps {
  tag: Tag
  title?: string
}

export function TagChip({ tag, title }: TagChipProps) {
  const hoverText = title ?? (tag.description || tag.name)
  return (
    <span
      className={`rounded px-1.5 py-0.5 text-[10px] font-mono ${TAG_COLOR_CLASSES[tag.color]}`}
      title={hoverText}
    >
      {tag.name}
    </span>
  )
}
