import { BrowserRouter, Routes, Route, Navigate, useNavigate, useLocation } from 'react-router-dom'
import { LayoutList, Play, BarChart3, Settings, Cog, FileText, Inbox, FolderTree, Sparkles, FolderKanban } from 'lucide-react'
import { NavRail, TooltipProvider, Toaster, type NavRailItem } from '@hollis-labs/sysop-ui'
import { ApiProvider } from '@/hooks/use-api'
import { ActiveRunsProvider } from '@/hooks/use-active-runs'
import BoardPage from '@/pages/BoardPage'
import TaskDetailPage from '@/pages/TaskDetailPage'
import RunsPage from '@/pages/RunsPage'
import DashboardPage from '@/pages/DashboardPage'
import WidgetPreviewPage from '@/pages/WidgetPreviewPage'
import SettingsPage from '@/pages/SettingsPage'
import ProjectDetailPage from '@/pages/ProjectDetailPage'
import ProjectsPage from '@/pages/ProjectsPage'
import SprintDetailPage from '@/pages/SprintDetailPage'
import SprintsPage from '@/pages/SprintsPage'
import EpicDetailPage from '@/pages/EpicDetailPage'
import EpicsPage from '@/pages/EpicsPage'
import ProjectEditPage from '@/pages/ProjectEditPage'
import SprintEditPage from '@/pages/SprintEditPage'
import EpicEditPage from '@/pages/EpicEditPage'
import TemplatesPage from '@/pages/TemplatesPage'
import TemplateDetailPage from '@/pages/TemplateDetailPage'
import CheckpointsPage from '@/pages/CheckpointsPage'
import PlansPage from '@/pages/PlansPage'
import PlanDetailPage from '@/pages/PlanDetailPage'
import ModelsPage from '@/pages/ModelsPage'
import CollectionsPage from '@/pages/CollectionsPage'

const NAV_DESTINATIONS = [
  { key: 'operations', to: '/operations', label: 'Operations', icon: <LayoutList className="h-4 w-4" /> },
  { key: 'collections', to: '/collections', label: 'Collections', icon: <FolderKanban className="h-4 w-4" /> },
  { key: 'plans', to: '/plans', label: 'Plans', icon: <FolderTree className="h-4 w-4" /> },
  { key: 'templates', to: '/templates', label: 'Templates', icon: <FileText className="h-4 w-4" /> },
  { key: 'checkpoints', to: '/checkpoints', label: 'Checkpoints', icon: <Inbox className="h-4 w-4" /> },
  { key: 'runs', to: '/runs', label: 'Runs', icon: <Play className="h-4 w-4" /> },
  { key: 'dashboard', to: '/dashboard', label: 'Dashboard', icon: <BarChart3 className="h-4 w-4" /> },
  { key: 'models', to: '/models', label: 'Models', icon: <Sparkles className="h-4 w-4" /> },
  { key: 'settings', to: '/settings', label: 'Settings', icon: <Settings className="h-4 w-4" />, footer: true },
] as const

function AppShell() {
  const navigate = useNavigate()
  const location = useLocation()

  const navItems: NavRailItem[] = NAV_DESTINATIONS.map((dest) => ({
    key: dest.key,
    label: dest.label,
    icon: dest.icon,
    footer: 'footer' in dest ? dest.footer : undefined,
    active:
      dest.to === '/operations'
        ? location.pathname === '/operations' || location.pathname === '/'
        : location.pathname.startsWith(dest.to),
    onSelect: () => navigate(dest.to),
  }))

  return (
    <div className="flex h-full w-full overflow-hidden bg-bg text-foreground">
      <NavRail items={navItems} logo={<Cog className="h-5 w-5" />} logoLabel="Torque" />

      {/* Main content */}
      <main className="flex-1 overflow-auto bg-bg">
        <Routes>
          <Route path="/" element={<Navigate to="/operations" replace />} />
          <Route path="/operations" element={<BoardPage />} />
          <Route path="/collections" element={<CollectionsPage />} />
          <Route path="/tasks/:id" element={<TaskDetailPage />} />
          <Route path="/projects" element={<ProjectsPage />} />
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
          <Route path="/projects/:id/edit" element={<ProjectEditPage />} />
          <Route path="/sprints" element={<SprintsPage />} />
          <Route path="/sprints/:id" element={<SprintDetailPage />} />
          <Route path="/sprints/:id/edit" element={<SprintEditPage />} />
          <Route path="/epics" element={<EpicsPage />} />
          <Route path="/epics/:id" element={<EpicDetailPage />} />
          <Route path="/epics/:id/edit" element={<EpicEditPage />} />
          <Route path="/plans" element={<PlansPage />} />
          <Route path="/plans/:id" element={<PlanDetailPage />} />
          <Route path="/templates" element={<TemplatesPage />} />
          <Route path="/templates/:id" element={<TemplateDetailPage />} />
          <Route path="/checkpoints" element={<CheckpointsPage />} />
          <Route path="/runs" element={<RunsPage />} />
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/dashboard/_widget-preview" element={<WidgetPreviewPage />} />
          <Route path="/models" element={<ModelsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Routes>
      </main>
    </div>
  )
}

export default function App() {
  return (
    <ApiProvider>
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
