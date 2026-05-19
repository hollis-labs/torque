import { useState } from 'react'
import { Send } from 'lucide-react'
import { Card, CardContent, CardHeader, Textarea, Button, Skeleton } from '@hollis-labs/sysop-ui'
import { formatRelativeTime } from '@/lib/utils'
import type { Comment } from '@/lib/types'

interface CommentListProps {
  comments: Comment[]
  loading?: boolean
  onAddComment: (content: string) => Promise<void>
}

export function CommentList({ comments, loading, onAddComment }: CommentListProps) {
  const [draft, setDraft] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const content = draft.trim()
    if (!content) return
    setSubmitting(true)
    try {
      await onAddComment(content)
      setDraft('')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      {loading ? (
        Array.from({ length: 2 }).map((_, i) => (
          <Skeleton key={i} className="h-20 w-full rounded-lg" />
        ))
      ) : comments.length === 0 ? (
        <p className="py-8 text-center text-sm text-muted-foreground">No comments yet.</p>
      ) : (
        comments.map((comment) => (
          <Card key={comment.id} className="gap-2">
            <CardHeader className="pb-0">
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium text-foreground">
                  {comment.author || 'Anonymous'}
                </span>
                <span className="text-xs text-muted-foreground">
                  {formatRelativeTime(comment.created_at)}
                </span>
              </div>
            </CardHeader>
            <CardContent>
              <p className="text-sm text-foreground whitespace-pre-wrap">{comment.content}</p>
            </CardContent>
          </Card>
        ))
      )}

      {/* Add comment form */}
      <form onSubmit={handleSubmit} className="flex flex-col gap-2 pt-2 border-t border-border">
        <Textarea
          placeholder="Add a comment…"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          rows={3}
          className="resize-none text-sm"
          disabled={submitting}
        />
        <div className="flex justify-end">
          <Button type="submit" size="sm" disabled={!draft.trim() || submitting}>
            <Send className="mr-1.5 h-3.5 w-3.5" />
            {submitting ? 'Posting…' : 'Post comment'}
          </Button>
        </div>
      </form>
    </div>
  )
}
