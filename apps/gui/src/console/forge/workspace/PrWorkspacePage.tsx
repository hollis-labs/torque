import { Link, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ChevronLeft } from 'lucide-react'
import { PrWorkspace } from './PrWorkspace'
import { prRefFromParams } from './route'

/** Route element for PR_WORKSPACE_ROUTE. `?file=` deep-links a file; `?readonly=1` hides writes. */
export default function PrWorkspacePage() {
  const params = useParams()
  const [search] = useSearchParams()
  const prRef = prRefFromParams(params)
  const navigate = useNavigate()
  // Arrived from the inbox, a chip or a link: Back returns there. A fresh load falls back to the planner.
  const canGoBack = useLocation().key !== 'default'
  if (!prRef) {
    return (
      <div className="p-6" role="alert">
        <h1 className="font-mono text-sm uppercase tracking-widest text-red-300">Invalid pull request link</h1>
      </div>
    )
  }
  return (
    <div className="flex h-full flex-col">
      <div className="shrink-0 border-b border-zinc-800/80 px-4 py-1.5">
        <Link to="/planner" onClick={canGoBack ? (e) => { e.preventDefault(); navigate(-1) } : undefined} className="inline-flex items-center gap-1 font-mono text-[10px] uppercase tracking-widest text-zinc-500 hover:text-zinc-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">
          <ChevronLeft className="h-3 w-3" /> Back
        </Link>
      </div>
      <PrWorkspace className="min-h-0 flex-1" prRef={prRef} initialFile={search.get('file') ?? undefined} readOnly={search.get('readonly') === '1'} />
    </div>
  )
}
