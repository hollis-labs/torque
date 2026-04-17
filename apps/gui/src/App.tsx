import { BrowserRouter, Routes, Route, NavLink, Navigate } from 'react-router-dom'
import { LayoutList, Play, BarChart3, Settings, Cog, FileText, Inbox } from 'lucide-react'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from '@/components/ui/sonner'
import { ApiProvider } from '@/hooks/use-api'
import { ActiveRunsProvider } from '@/hooks/use-active-runs'
import BoardPage from '@/pages/BoardPage'
import TaskDetailPage from '@/pages/TaskDetailPage'
import RunsPage from '@/pages/RunsPage'
import DashboardPage from '@/pages/DashboardPage'
import WidgetPreviewPage from '@/pages/WidgetPreviewPage'
import SettingsPage from '@/pages/SettingsPage'
import ProjectDetailPage from '@/pages/ProjectDetailPage'
import SprintDetailPage from '@/pages/SprintDetailPage'
import EpicDetailPage from '@/pages/EpicDetailPage'
import TemplatesPage from '@/pages/TemplatesPage'
import TemplateDetailPage from '@/pages/TemplateDetailPage'
import CheckpointsPage from '@/pages/CheckpointsPage'
import { cn } from '@/lib/utils'

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL as string | undefined

function NavItem({
  to,
  label,
  children,
}: {
  to: string
  label: string
  children: React.ReactNode
}) {
  return (
    <NavLink
      to={to}
      end={to === '/operations'}
      title={label}
      className={({ isActive }) =>
        cn(
          'flex h-9 w-9 items-center justify-center rounded-md transition-colors',
          'text-zinc-500 hover:bg-zinc-900 hover:text-zinc-100',
          isActive && 'bg-zinc-900 text-zinc-100'
        )
      }
    >
      {children}
    </NavLink>
  )
}

function AppShell() {
  return (
    <div className="flex h-screen w-screen overflow-hidden bg-zinc-950 text-zinc-100">
      {/* Nav rail */}
      <nav className="flex w-14 flex-col items-center gap-2 border-r border-zinc-800 bg-zinc-950 py-4">
        {/* Logo */}
        <div
          className="mb-2 flex h-9 w-9 items-center justify-center rounded-md border border-zinc-800 bg-zinc-900 text-zinc-300"
          title="Clockwork Manifold"
        >
          <Cog className="h-5 w-5" />
        </div>

        <div className="h-px w-8 bg-zinc-800 mb-1" />

        <NavItem to="/operations" label="Operations">
          <LayoutList className="h-4 w-4" />
        </NavItem>
        <NavItem to="/templates" label="Templates">
          <FileText className="h-4 w-4" />
        </NavItem>
        <NavItem to="/checkpoints" label="Checkpoints">
          <Inbox className="h-4 w-4" />
        </NavItem>
        <NavItem to="/runs" label="Runs">
          <Play className="h-4 w-4" />
        </NavItem>
        <NavItem to="/dashboard" label="Dashboard">
          <BarChart3 className="h-4 w-4" />
        </NavItem>

        <div className="mt-auto" />

        <NavItem to="/settings" label="Settings">
          <Settings className="h-4 w-4" />
        </NavItem>
      </nav>

      {/* Main content */}
      <main className="flex-1 overflow-auto bg-zinc-950">
        <Routes>
          <Route path="/" element={<Navigate to="/operations" replace />} />
          <Route path="/operations" element={<BoardPage />} />
          <Route path="/tasks/:id" element={<TaskDetailPage />} />
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
          <Route path="/sprints/:id" element={<SprintDetailPage />} />
          <Route path="/epics/:id" element={<EpicDetailPage />} />
          <Route path="/templates" element={<TemplatesPage />} />
          <Route path="/templates/:id" element={<TemplateDetailPage />} />
          <Route path="/checkpoints" element={<CheckpointsPage />} />
          <Route path="/runs" element={<RunsPage />} />
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/dashboard/_widget-preview" element={<WidgetPreviewPage />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Routes>
      </main>
    </div>
  )
}

export default function App() {
  return (
    <ApiProvider baseUrl={API_BASE_URL}>
      <ActiveRunsProvider>
        <TooltipProvider>
          <BrowserRouter>
            <AppShell />
          </BrowserRouter>
          <Toaster position="bottom-right" />
        </TooltipProvider>
      </ActiveRunsProvider>
    </ApiProvider>
  )
}
