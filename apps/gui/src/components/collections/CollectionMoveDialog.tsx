import { useState } from 'react'
import { Button, Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, Input } from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { usePagedList } from '@/hooks/use-paged-list'
import { useListSearch } from '@/hooks/use-list-search'
import { ListPageControls } from '@/components/domain/list-page-controls'

export function CollectionMoveDialog({ count, busy, onClose, onChoose }: { count: number; busy: boolean; onClose: () => void; onChoose: (id: string) => void }) {
  const api = useApi()
  const [input, setInput] = useState('')
  const search = useListSearch(input)
  const page = usePagedList({
    params: { search, sort_by: 'name', sort_dir: 'asc' as const, include_total: true },
    fetchPage: ({ params, cursor, signal }) => api.listCollections('active', { ...params, cursor }, signal),
    getId: collection => collection.id,
  })
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}>
    <DialogContent className="max-h-[80vh] overflow-auto">
      <DialogHeader><DialogTitle>Move {count} selected {count === 1 ? 'task' : 'tasks'}</DialogTitle><DialogDescription>Choose an active collection. Search runs on the server.</DialogDescription></DialogHeader>
      <Input aria-label="Search destination collections" value={input} onChange={event => setInput(event.target.value)} />
      <Button variant="outline" disabled={busy} onClick={() => onChoose('')}>Move to inbox</Button>
      {page.items.map(collection => <Button key={collection.id} variant="ghost" disabled={busy || input.trim() !== search} onClick={() => onChoose(collection.id)}>{collection.name}</Button>)}
      <ListPageControls page={page} label="destination collections" />
      <Button variant="outline" disabled={busy} onClick={onClose}>Cancel</Button>
    </DialogContent>
  </Dialog>
}
