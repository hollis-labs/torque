import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { BookOpen, ExternalLink, FileText, FolderOpen, FolderTree, Plus, Trash2 } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useApi } from '@/hooks/use-api'
import { isHtmlApiFallbackError } from '@/lib/api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Epic, FeatureFlags, Project, ProjectArtifact, Sprint } from '@/lib/types'

interface ScopeManagerDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  flags: FeatureFlags
  onDataChange?: () => void
}

type ScopeTab = 'projects' | 'epics' | 'sprints'

type ProjectDraft = {
  id?: string
  name: string
  description: string
  repoPath: string
  agentPath: string
  readPaths: string
  writePaths: string
  contextPaths: string
  permissions: string
  rules: string
  status: 'active' | 'inactive'
  icon: string
}

type ArtifactDraft = {
  id?: number
  entryType: string
  title: string
  description: string
  filePath: string
  url: string
  content: string
  permissions: string
  rules: string
}

type EpicDraft = {
  id?: string
  name: string
  description: string
  status: 'active' | 'inactive'
  projectId: string
  priority: string
}

type SprintDraft = {
  id?: string
  name: string
  goal: string
  status: 'active' | 'inactive'
  projectId: string
  approvalMode: string
  costBudget: string
}

const NONE = '__none__'

function emptyProjectDraft(): ProjectDraft {
  return {
    name: '',
    description: '',
    repoPath: '',
    agentPath: '',
    readPaths: '',
    writePaths: '',
    contextPaths: '',
    permissions: '',
    rules: '',
    status: 'active',
    icon: '',
  }
}

function emptyArtifactDraft(): ArtifactDraft {
  return {
    entryType: 'document',
    title: '',
    description: '',
    filePath: '',
    url: '',
    content: '',
    permissions: '',
    rules: '',
  }
}

function emptyEpicDraft(): EpicDraft {
  return {
    name: '',
    description: '',
    status: 'active',
    projectId: NONE,
    priority: '',
  }
}

function emptySprintDraft(): SprintDraft {
  return {
    name: '',
    goal: '',
    status: 'active',
    projectId: NONE,
    approvalMode: 'approve_each',
    costBudget: '',
  }
}

function linesToList(value: string): string[] {
  return value
    .split('\n')
    .map((item) => item.trim())
    .filter(Boolean)
}

function listToLines(value: string[] | undefined): string {
  return (value ?? []).join('\n')
}

function permissionsToLines(value: Record<string, string> | undefined): string {
  return Object.entries(value ?? {})
    .map(([key, val]) => `${key}=${val}`)
    .join('\n')
}

function linesToPermissions(value: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of value.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const idx = trimmed.indexOf('=')
    if (idx === -1) {
      out[trimmed] = 'allow'
      continue
    }
    const key = trimmed.slice(0, idx).trim()
    const val = trimmed.slice(idx + 1).trim()
    if (key) out[key] = val || 'allow'
  }
  return out
}

function projectToDraft(project: Project): ProjectDraft {
  return {
    id: project.id,
    name: project.name,
    description: project.description,
    repoPath: project.repo_path,
    agentPath: project.agent_path,
    readPaths: listToLines(project.read_paths),
    writePaths: listToLines(project.write_paths),
    contextPaths: listToLines(project.context_paths),
    permissions: permissionsToLines(project.permissions),
    rules: listToLines(project.rules),
    status: project.status,
    icon: project.icon,
  }
}

function artifactToDraft(artifact: ProjectArtifact): ArtifactDraft {
  return {
    id: artifact.id,
    entryType: artifact.entry_type,
    title: artifact.title,
    description: artifact.description,
    filePath: artifact.file_path,
    url: artifact.url,
    content: artifact.content,
    permissions: permissionsToLines(artifact.permissions),
    rules: listToLines(artifact.rules),
  }
}

function epicToDraft(epic: Epic): EpicDraft {
  return {
    id: epic.id,
    name: epic.name,
    description: epic.description,
    status: epic.status,
    projectId: epic.project_id ?? NONE,
    priority: epic.priority === null || epic.priority === undefined ? '' : String(epic.priority),
  }
}

function sprintToDraft(sprint: Sprint): SprintDraft {
  return {
    id: sprint.id,
    name: sprint.name,
    goal: sprint.goal ?? '',
    status: sprint.status,
    projectId: sprint.project_id ?? NONE,
    approvalMode: sprint.approval_mode || 'approve_each',
    costBudget: sprint.cost_budget === null || sprint.cost_budget === undefined ? '' : String(sprint.cost_budget),
  }
}

export function ScopeManagerDialog({ open, onOpenChange, flags, onDataChange }: ScopeManagerDialogProps) {
  const api = useApi()
  const navigate = useNavigate()

  const availableTabs = useMemo<ScopeTab[]>(() => {
    const tabs: ScopeTab[] = []
    if (flags.projects) tabs.push('projects')
    if (flags.epics) tabs.push('epics')
    if (flags.sprints) tabs.push('sprints')
    return tabs
  }, [flags])

  const [activeTab, setActiveTab] = useState<ScopeTab>('projects')
  const [loading, setLoading] = useState(false)

  const [projects, setProjects] = useState<Project[]>([])
  const [epics, setEpics] = useState<Epic[]>([])
  const [sprints, setSprints] = useState<Sprint[]>([])
  const [projectArtifacts, setProjectArtifacts] = useState<ProjectArtifact[]>([])
  const [artifactRouteWarning, setArtifactRouteWarning] = useState<string | null>(null)

  const [selectedProjectId, setSelectedProjectId] = useState<string | null | undefined>(undefined)
  const [selectedEpicId, setSelectedEpicId] = useState<string | null | undefined>(undefined)
  const [selectedSprintId, setSelectedSprintId] = useState<string | null | undefined>(undefined)

  const [projectDraft, setProjectDraft] = useState<ProjectDraft>(emptyProjectDraft())
  const [artifactDraft, setArtifactDraft] = useState<ArtifactDraft>(emptyArtifactDraft())
  const [epicDraft, setEpicDraft] = useState<EpicDraft>(emptyEpicDraft())
  const [sprintDraft, setSprintDraft] = useState<SprintDraft>(emptySprintDraft())

  const [saving, setSaving] = useState(false)
  const [showInactive, setShowInactive] = useState(false)
  const [sortOrder, setSortOrder] = useState<'title' | 'updated'>('updated')

  useEffect(() => {
    if (availableTabs.length > 0 && !availableTabs.includes(activeTab)) {
      setActiveTab(availableTabs[0])
    }
  }, [activeTab, availableTabs])

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setLoading(true)
    void (async () => {
      try {
        const [projectRes, epicRes, sprintRes] = await Promise.all([
          flags.projects ? api.listProjects() : Promise.resolve({ projects: [] as Project[] }),
          flags.epics ? api.listEpics() : Promise.resolve({ epics: [] as Epic[] }),
          flags.sprints ? api.listSprints() : Promise.resolve({ sprints: [] as Sprint[] }),
        ])
        if (cancelled) return
        setProjects(projectRes.projects)
        setEpics(epicRes.epics)
        setSprints(sprintRes.sprints)
        const firstProject = projectRes.projects[0] ?? null
        const firstEpic = epicRes.epics[0] ?? null
        const firstSprint = sprintRes.sprints[0] ?? null
        setSelectedProjectId((prev) => prev === undefined ? firstProject?.id ?? null : prev)
        setSelectedEpicId((prev) => prev === undefined ? firstEpic?.id ?? null : prev)
        setSelectedSprintId((prev) => prev === undefined ? firstSprint?.id ?? null : prev)
      } catch (err) {
        notifyError(err, 'Failed to load scope manager')
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [api, flags.projects, flags.epics, flags.sprints, open])

  const selectedProject = projects.find((project) => project.id === selectedProjectId) ?? null
  const selectedEpic = epics.find((epic) => epic.id === selectedEpicId) ?? null
  const selectedSprint = sprints.find((sprint) => sprint.id === selectedSprintId) ?? null
  const visibleProjects = useMemo(() => sortItems(projects, showInactive, sortOrder), [projects, showInactive, sortOrder])
  const visibleEpics = useMemo(() => sortItems(epics, showInactive, sortOrder), [epics, showInactive, sortOrder])
  const visibleSprints = useMemo(() => sortItems(sprints, showInactive, sortOrder), [sprints, showInactive, sortOrder])

  useEffect(() => {
    setProjectDraft(selectedProject ? projectToDraft(selectedProject) : emptyProjectDraft())
  }, [selectedProject])

  useEffect(() => {
    setEpicDraft(selectedEpic ? epicToDraft(selectedEpic) : emptyEpicDraft())
  }, [selectedEpic])

  useEffect(() => {
    setSprintDraft(selectedSprint ? sprintToDraft(selectedSprint) : emptySprintDraft())
  }, [selectedSprint])

  useEffect(() => {
    if (!selectedProjectId) {
      setProjectArtifacts([])
      setArtifactDraft(emptyArtifactDraft())
      setArtifactRouteWarning(null)
      return
    }
    let cancelled = false
    void api.listProjectArtifacts(selectedProjectId)
      .then((res) => {
        if (!cancelled) {
          setProjectArtifacts(res.artifacts)
          setArtifactRouteWarning(null)
        }
      })
      .catch((err) => {
        if (cancelled) return
        if (isHtmlApiFallbackError(err) || (err instanceof Error && /HTTP 404|not found/i.test(err.message))) {
          setProjectArtifacts([])
          setArtifactRouteWarning('Project artifacts are unavailable from the current backend runtime. The API route is missing or stale.')
          return
        }
        notifyError(err, 'Failed to load project artifacts')
      })
    return () => {
      cancelled = true
    }
  }, [api, selectedProjectId])

  function closeAndNavigate(url: string) {
    onOpenChange(false)
    navigate(url)
  }

  async function refreshProjects(selectId?: string | null) {
    const res = await api.listProjects()
    setProjects(res.projects)
    const nextId = selectId ?? selectedProjectId
    if (nextId === null) {
      setSelectedProjectId(null)
    } else {
      setSelectedProjectId(nextId && res.projects.some((project) => project.id === nextId) ? nextId : res.projects[0]?.id ?? null)
    }
    onDataChange?.()
  }

  async function refreshEpics(selectId?: string | null) {
    const res = await api.listEpics()
    setEpics(res.epics)
    const nextId = selectId ?? selectedEpicId
    if (nextId === null) {
      setSelectedEpicId(null)
    } else {
      setSelectedEpicId(nextId && res.epics.some((epic) => epic.id === nextId) ? nextId : res.epics[0]?.id ?? null)
    }
    onDataChange?.()
  }

  async function refreshSprints(selectId?: string | null) {
    const res = await api.listSprints()
    setSprints(res.sprints)
    const nextId = selectId ?? selectedSprintId
    if (nextId === null) {
      setSelectedSprintId(null)
    } else {
      setSelectedSprintId(nextId && res.sprints.some((sprint) => sprint.id === nextId) ? nextId : res.sprints[0]?.id ?? null)
    }
    onDataChange?.()
  }

  async function saveProject() {
    const payload = {
      name: projectDraft.name.trim(),
      description: projectDraft.description.trim(),
      repo_path: projectDraft.repoPath.trim(),
      agent_path: projectDraft.agentPath.trim(),
      read_paths: linesToList(projectDraft.readPaths),
      write_paths: linesToList(projectDraft.writePaths),
      context_paths: linesToList(projectDraft.contextPaths),
      permissions: linesToPermissions(projectDraft.permissions),
      rules: linesToList(projectDraft.rules),
      status: projectDraft.status,
      icon: projectDraft.icon.trim(),
    }
    if (!payload.name || !payload.repo_path) return
    setSaving(true)
    try {
      if (projectDraft.id) {
        const updated = await api.updateProject(projectDraft.id, payload)
        notifySuccess(`Updated ${updated.name}`)
        await refreshProjects(updated.id)
      } else {
        const created = await api.createProject(payload)
        notifySuccess(`Created ${created.name}`)
        await refreshProjects(created.id)
      }
    } catch (err) {
      notifyError(err, 'Failed to save project')
    } finally {
      setSaving(false)
    }
  }

  async function deleteProject() {
    if (!projectDraft.id) return
    if (!window.confirm(`Delete project ${projectDraft.name}?`)) return
    setSaving(true)
    try {
      await api.deleteProject(projectDraft.id)
      notifySuccess(`Deleted ${projectDraft.name}`)
      await refreshProjects(null)
      setProjectDraft(emptyProjectDraft())
    } catch (err) {
      notifyError(err, 'Failed to delete project')
    } finally {
      setSaving(false)
    }
  }

  async function saveArtifact() {
    if (!selectedProjectId) return
    const payload = {
      entry_type: artifactDraft.entryType,
      title: artifactDraft.title.trim(),
      description: artifactDraft.description.trim(),
      file_path: artifactDraft.filePath.trim(),
      url: artifactDraft.url.trim(),
      content: artifactDraft.content.trim(),
      permissions: linesToPermissions(artifactDraft.permissions),
      rules: linesToList(artifactDraft.rules),
    }
    if (!payload.file_path) return
    setSaving(true)
    try {
      if (artifactDraft.id) {
        await api.updateProjectArtifact(selectedProjectId, artifactDraft.id, payload)
        notifySuccess(`Updated ${payload.file_path}`)
      } else {
        await api.createProjectArtifact(selectedProjectId, payload)
        notifySuccess(`Added ${payload.file_path}`)
      }
      const res = await api.listProjectArtifacts(selectedProjectId)
      setProjectArtifacts(res.artifacts)
      setArtifactDraft(emptyArtifactDraft())
      onDataChange?.()
    } catch (err) {
      notifyError(err, 'Failed to save project artifact')
    } finally {
      setSaving(false)
    }
  }

  async function deleteArtifact(artifact: ProjectArtifact) {
    if (!selectedProjectId) return
    if (!window.confirm(`Delete ${artifact.file_path}?`)) return
    setSaving(true)
    try {
      await api.deleteProjectArtifact(selectedProjectId, artifact.id)
      setProjectArtifacts((prev) => prev.filter((item) => item.id !== artifact.id))
      if (artifactDraft.id === artifact.id) setArtifactDraft(emptyArtifactDraft())
      notifySuccess(`Deleted ${artifact.file_path}`)
      onDataChange?.()
    } catch (err) {
      notifyError(err, 'Failed to delete project artifact')
    } finally {
      setSaving(false)
    }
  }

  async function saveEpic() {
    const payload = {
      name: epicDraft.name.trim(),
      description: epicDraft.description.trim(),
      status: epicDraft.status,
      project_id: epicDraft.projectId === NONE ? null : epicDraft.projectId,
      priority: epicDraft.priority.trim() ? Number(epicDraft.priority.trim()) : null,
    }
    if (!payload.name) return
    setSaving(true)
    try {
      if (epicDraft.id) {
        const updated = await api.updateEpic(epicDraft.id, payload)
        notifySuccess(`Updated ${updated.name}`)
        await refreshEpics(updated.id)
      } else {
        const created = await api.createEpic(payload)
        notifySuccess(`Created ${created.name}`)
        await refreshEpics(created.id)
      }
    } catch (err) {
      notifyError(err, 'Failed to save epic')
    } finally {
      setSaving(false)
    }
  }

  async function deleteEpic() {
    if (!epicDraft.id) return
    if (!window.confirm(`Delete epic ${epicDraft.name}?`)) return
    setSaving(true)
    try {
      await api.deleteEpic(epicDraft.id)
      notifySuccess(`Deleted ${epicDraft.name}`)
      await refreshEpics(null)
      setEpicDraft(emptyEpicDraft())
    } catch (err) {
      notifyError(err, 'Failed to delete epic')
    } finally {
      setSaving(false)
    }
  }

  async function saveSprint() {
    const desiredStatus = sprintDraft.status
    const payload = {
      name: sprintDraft.name.trim(),
      goal: sprintDraft.goal.trim(),
      project_id: sprintDraft.projectId === NONE ? null : sprintDraft.projectId,
      approval_mode: sprintDraft.approvalMode,
      cost_budget: sprintDraft.costBudget.trim() ? Number(sprintDraft.costBudget.trim()) : null,
    }
    if (!payload.name) return
    setSaving(true)
    try {
      if (sprintDraft.id) {
        const updated = await api.updateSprint(sprintDraft.id, payload)
        if (selectedSprint && desiredStatus !== selectedSprint.status) {
          await api.transitionSprint(sprintDraft.id, desiredStatus)
        }
        notifySuccess(`Updated ${updated.name}`)
        await refreshSprints(updated.id)
      } else {
        const created = await api.createSprint(payload)
        notifySuccess(`Created ${created.name}`)
        await refreshSprints(created.id)
      }
    } catch (err) {
      notifyError(err, 'Failed to save sprint')
    } finally {
      setSaving(false)
    }
  }

  async function deleteSprint() {
    if (!sprintDraft.id) return
    if (!window.confirm(`Delete sprint ${sprintDraft.name}?`)) return
    setSaving(true)
    try {
      await api.deleteSprint(sprintDraft.id)
      notifySuccess(`Deleted ${sprintDraft.name}`)
      await refreshSprints(null)
      setSprintDraft(emptySprintDraft())
    } catch (err) {
      notifyError(err, 'Failed to delete sprint')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[90vh] max-h-[90vh] w-[min(96vw,96rem)] max-w-[min(96vw,96rem)] sm:max-w-[min(96vw,96rem)] flex-col overflow-hidden">
        <DialogHeader>
          <DialogTitle>Scope Manager</DialogTitle>
          <DialogDescription>
            Manage projects, epics, and sprints without leaving Operations. Use the scoped links to jump back into the task view.
          </DialogDescription>
        </DialogHeader>

        <Tabs value={activeTab} onValueChange={(value) => setActiveTab(value as ScopeTab)} className="flex min-h-0 flex-1 flex-col overflow-hidden">
          <TabsList className="mb-4 w-fit">
            {flags.projects && <TabsTrigger value="projects">Projects</TabsTrigger>}
            {flags.epics && <TabsTrigger value="epics">Epics</TabsTrigger>}
            {flags.sprints && <TabsTrigger value="sprints">Sprints</TabsTrigger>}
          </TabsList>

          <TabsContent value="projects" className="min-h-0 flex-1 overflow-hidden">
            <div className="grid h-full min-h-0 grid-cols-[280px,minmax(0,1fr)] gap-4 overflow-hidden">
              <div className="flex min-h-0 min-w-0 flex-col rounded-xl border border-zinc-800 bg-zinc-950/60">
                <ListToolbar
                  title="Projects"
                  showInactive={showInactive}
                  sortOrder={sortOrder}
                  onToggleInactive={setShowInactive}
                  onSortChange={setSortOrder}
                  onCreate={() => { setSelectedProjectId(null); setProjectDraft(emptyProjectDraft()); setArtifactDraft(emptyArtifactDraft()) }}
                />
                <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto p-2" tabIndex={0}>
                  {visibleProjects.map((project) => (
                    <button
                      key={project.id}
                      type="button"
                      onClick={() => setSelectedProjectId(project.id)}
                      className={`mb-2 flex w-full flex-col rounded-lg border px-3 py-2 text-left transition-colors ${
                        selectedProjectId === project.id ? 'border-zinc-500 bg-zinc-900 text-zinc-100' : 'border-zinc-800 bg-zinc-950/50 text-zinc-300 hover:bg-zinc-900/70'
                      }`}
                    >
                      <span className="text-sm font-medium">{project.name}</span>
                      <span className="text-[11px] text-zinc-500">{project.id}</span>
                    </button>
                  ))}
                  {!loading && visibleProjects.length === 0 && (
                    <div className="px-2 py-4 text-sm text-zinc-500">No projects yet.</div>
                  )}
                </div>
              </div>

              <div className="no-scrollbar min-h-0 min-w-0 overflow-y-auto rounded-xl border border-zinc-800 bg-zinc-950/60 p-4" tabIndex={0}>
                <div className="mb-4 flex items-center justify-between gap-3">
                  <div>
                    <h3 className="text-base font-semibold text-zinc-100">{projectDraft.id ? projectDraft.name || 'Project' : 'New project'}</h3>
                    <p className="text-sm text-zinc-500">Projects define the execution context tasks can inherit.</p>
                  </div>
                  {projectDraft.id && (
                    <div className="flex items-center gap-2">
                      <Button variant="outline" size="sm" onClick={() => closeAndNavigate(`/projects/${projectDraft.id}`)}>
                        <FolderTree className="h-3.5 w-3.5" />
                        Details
                      </Button>
                      <Button variant="outline" size="sm" onClick={() => closeAndNavigate(`/operations?project_id=${projectDraft.id}`)}>
                        <ExternalLink className="h-3.5 w-3.5" />
                        Open in Operations
                      </Button>
                    </div>
                  )}
                </div>

                <div className="grid gap-4 md:grid-cols-2">
                  <Field label="Name *">
                    <Input value={projectDraft.name} onChange={(e) => setProjectDraft((prev) => ({ ...prev, name: e.target.value }))} placeholder="Project name" />
                  </Field>
                  <Field label="Status">
                    <select
                      className="h-8 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-sm text-zinc-100"
                      value={projectDraft.status}
                      onChange={(e) => setProjectDraft((prev) => ({ ...prev, status: e.target.value as 'active' | 'inactive' }))}
                    >
                      <option value="active">Active</option>
                      <option value="inactive">Inactive</option>
                    </select>
                  </Field>
                  <Field label="Project path *">
                    <Input value={projectDraft.repoPath} onChange={(e) => setProjectDraft((prev) => ({ ...prev, repoPath: e.target.value }))} placeholder="/Users/you/Projects/app" />
                  </Field>
                  <Field label="Agent path">
                    <Input value={projectDraft.agentPath} onChange={(e) => setProjectDraft((prev) => ({ ...prev, agentPath: e.target.value }))} placeholder=".agents/project.md or .agents/" />
                  </Field>
                  <Field label="Icon">
                    <Input value={projectDraft.icon} onChange={(e) => setProjectDraft((prev) => ({ ...prev, icon: e.target.value }))} placeholder="AB" />
                  </Field>
                  <Field label="Description" className="md:col-span-2">
                    <Textarea value={projectDraft.description} onChange={(e) => setProjectDraft((prev) => ({ ...prev, description: e.target.value }))} rows={3} placeholder="Project summary, conventions, and intent." />
                  </Field>
                  <Field label="Read paths" className="md:col-span-2">
                    <Textarea value={projectDraft.readPaths} onChange={(e) => setProjectDraft((prev) => ({ ...prev, readPaths: e.target.value }))} rows={3} placeholder="One path per line" />
                  </Field>
                  <Field label="Write paths" className="md:col-span-2">
                    <Textarea value={projectDraft.writePaths} onChange={(e) => setProjectDraft((prev) => ({ ...prev, writePaths: e.target.value }))} rows={3} placeholder="One path per line" />
                  </Field>
                  <Field label="Additional context dirs" className="md:col-span-2">
                    <Textarea value={projectDraft.contextPaths} onChange={(e) => setProjectDraft((prev) => ({ ...prev, contextPaths: e.target.value }))} rows={3} placeholder="One path per line" />
                  </Field>
                  <Field label="Permissions" className="md:col-span-2">
                    <Textarea value={projectDraft.permissions} onChange={(e) => setProjectDraft((prev) => ({ ...prev, permissions: e.target.value }))} rows={3} placeholder={'sandbox=workspace-write\nnetwork=restricted'} />
                  </Field>
                  <Field label="Rules" className="md:col-span-2">
                    <Textarea value={projectDraft.rules} onChange={(e) => setProjectDraft((prev) => ({ ...prev, rules: e.target.value }))} rows={4} placeholder="One rule per line" />
                  </Field>
                </div>

                <div className="mt-4 flex items-center gap-2">
                  <Button onClick={saveProject} disabled={saving || !projectDraft.name.trim() || !projectDraft.repoPath.trim()}>
                    {projectDraft.id ? 'Save project' : 'Create project'}
                  </Button>
                  {projectDraft.id && (
                    <Button variant="destructive" onClick={deleteProject} disabled={saving}>
                      <Trash2 className="h-3.5 w-3.5" />
                      Delete
                    </Button>
                  )}
                </div>

                {projectDraft.id && (
                  <div className="mt-8 border-t border-zinc-800 pt-6">
                    <div className="mb-4 flex items-center justify-between">
                      <div>
                        <h4 className="text-sm font-semibold text-zinc-100">Artifacts</h4>
                        <p className="text-sm text-zinc-500">Folder and document references that tasks inherit as project context.</p>
                      </div>
                    </div>

                    {artifactRouteWarning && (
                      <div className="mb-4 rounded-lg border border-amber-700/50 bg-amber-950/30 px-3 py-2 text-sm text-amber-200">
                        {artifactRouteWarning}
                      </div>
                    )}

                    <div className="mb-4 overflow-hidden rounded-lg border border-zinc-800">
                      <div className="grid grid-cols-[120px,1fr,140px,120px] border-b border-zinc-800 bg-zinc-950 px-3 py-2 text-[11px] uppercase tracking-[0.18em] text-zinc-500">
                        <div>Type</div>
                        <div>Path</div>
                        <div>Title</div>
                        <div />
                      </div>
                      {projectArtifacts.length === 0 ? (
                        <div className="px-3 py-4 text-sm text-zinc-500">No project artifacts yet.</div>
                      ) : (
                        projectArtifacts.map((artifact) => (
                          <div key={artifact.id} className="grid grid-cols-[120px,1fr,140px,120px] items-center border-b border-zinc-800/70 px-3 py-2 text-sm last:border-b-0">
                            <div className="flex items-center gap-2 text-zinc-300">
                              {artifact.entry_type === 'folder' ? <FolderOpen className="h-4 w-4" /> : <FileText className="h-4 w-4" />}
                              <span>{artifact.entry_type}</span>
                            </div>
                            <div className="font-mono text-xs text-zinc-300">{artifact.file_path}</div>
                            <div className="truncate text-zinc-400">{artifact.title || 'Untitled'}</div>
                            <div className="flex items-center justify-end gap-2">
                              <Button variant="outline" size="sm" onClick={() => setArtifactDraft(artifactToDraft(artifact))}>Edit</Button>
                              <Button variant="destructive" size="sm" onClick={() => deleteArtifact(artifact)}>Delete</Button>
                            </div>
                          </div>
                        ))
                      )}
                    </div>

                    <div className="grid gap-4 md:grid-cols-2">
                      <Field label="Entry type">
                        <select
                          className="h-8 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-sm text-zinc-100"
                          value={artifactDraft.entryType}
                          onChange={(e) => setArtifactDraft((prev) => ({ ...prev, entryType: e.target.value }))}
                        >
                          <option value="document">Document</option>
                          <option value="folder">Folder</option>
                        </select>
                      </Field>
                      <Field label="Title">
                        <Input value={artifactDraft.title} onChange={(e) => setArtifactDraft((prev) => ({ ...prev, title: e.target.value }))} placeholder="PRD, screenshot set, design system..." />
                      </Field>
                      <Field label="File path *" className="md:col-span-2">
                        <Input value={artifactDraft.filePath} onChange={(e) => setArtifactDraft((prev) => ({ ...prev, filePath: e.target.value }))} placeholder="docs/prd.md or assets/screens/" />
                      </Field>
                      <Field label="Description" className="md:col-span-2">
                        <Textarea value={artifactDraft.description} onChange={(e) => setArtifactDraft((prev) => ({ ...prev, description: e.target.value }))} rows={3} placeholder="What this artifact is and when agents should use it." />
                      </Field>
                      <Field label="URL" className="md:col-span-2">
                        <Input value={artifactDraft.url} onChange={(e) => setArtifactDraft((prev) => ({ ...prev, url: e.target.value }))} placeholder="Optional external source" />
                      </Field>
                      <Field label="Permissions" className="md:col-span-2">
                        <Textarea value={artifactDraft.permissions} onChange={(e) => setArtifactDraft((prev) => ({ ...prev, permissions: e.target.value }))} rows={3} placeholder={'read=always\nshare=never'} />
                      </Field>
                      <Field label="Rules" className="md:col-span-2">
                        <Textarea value={artifactDraft.rules} onChange={(e) => setArtifactDraft((prev) => ({ ...prev, rules: e.target.value }))} rows={3} placeholder="One rule per line" />
                      </Field>
                    </div>

                    <div className="mt-4 flex items-center gap-2">
                      <Button onClick={saveArtifact} disabled={saving || !artifactDraft.filePath.trim()}>
                        {artifactDraft.id ? 'Save artifact' : 'Add artifact'}
                      </Button>
                      {artifactDraft.id && (
                        <Button variant="outline" onClick={() => setArtifactDraft(emptyArtifactDraft())}>
                          New artifact
                        </Button>
                      )}
                    </div>
                  </div>
                )}
              </div>
            </div>
          </TabsContent>

          <TabsContent value="epics" className="min-h-0 flex-1 overflow-hidden">
            <EntityLayout
              title="Epics"
              loading={loading}
              items={visibleEpics.map((epic) => ({ id: epic.id, label: epic.name, sublabel: epic.id }))}
              selectedId={selectedEpicId}
              onSelect={setSelectedEpicId}
              onCreate={() => { setSelectedEpicId(null); setEpicDraft(emptyEpicDraft()) }}
              showInactive={showInactive}
              sortOrder={sortOrder}
              onToggleInactive={setShowInactive}
              onSortChange={setSortOrder}
            >
              <EntityHeader
                title={epicDraft.id ? epicDraft.name || 'Epic' : 'New epic'}
                description="Epics stay lightweight here and link back into scoped Operations."
                detailButton={epicDraft.id ? { label: 'Details', icon: <BookOpen className="h-3.5 w-3.5" />, onClick: () => closeAndNavigate(`/epics/${epicDraft.id}`) } : undefined}
                opsButton={epicDraft.id ? { label: 'Open in Operations', onClick: () => closeAndNavigate(`/operations?epic_id=${epicDraft.id}`) } : undefined}
              />
              <div className="grid gap-4 md:grid-cols-2">
                <Field label="Name *">
                  <Input value={epicDraft.name} onChange={(e) => setEpicDraft((prev) => ({ ...prev, name: e.target.value }))} />
                </Field>
                <Field label="Status">
                  <select className="h-8 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-sm text-zinc-100" value={epicDraft.status} onChange={(e) => setEpicDraft((prev) => ({ ...prev, status: e.target.value as 'active' | 'inactive' }))}>
                    <option value="active">Active</option>
                    <option value="inactive">Inactive</option>
                  </select>
                </Field>
                <Field label="Project">
                  <select className="h-8 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-sm text-zinc-100" value={epicDraft.projectId} onChange={(e) => setEpicDraft((prev) => ({ ...prev, projectId: e.target.value }))}>
                    <option value={NONE}>No project</option>
                    {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
                  </select>
                </Field>
                <Field label="Priority">
                  <Input value={epicDraft.priority} onChange={(e) => setEpicDraft((prev) => ({ ...prev, priority: e.target.value }))} placeholder="1-3" />
                </Field>
                <Field label="Description" className="md:col-span-2">
                  <Textarea value={epicDraft.description} onChange={(e) => setEpicDraft((prev) => ({ ...prev, description: e.target.value }))} rows={4} />
                </Field>
              </div>
              <EntityActions
                onSave={saveEpic}
                onDelete={epicDraft.id ? deleteEpic : undefined}
                saveDisabled={saving || !epicDraft.name.trim()}
                saveLabel={epicDraft.id ? 'Save epic' : 'Create epic'}
              />
            </EntityLayout>
          </TabsContent>

          <TabsContent value="sprints" className="min-h-0 flex-1 overflow-hidden">
            <EntityLayout
              title="Sprints"
              loading={loading}
              items={visibleSprints.map((sprint) => ({ id: sprint.id, label: sprint.name, sublabel: sprint.id }))}
              selectedId={selectedSprintId}
              onSelect={setSelectedSprintId}
              onCreate={() => { setSelectedSprintId(null); setSprintDraft(emptySprintDraft()) }}
              showInactive={showInactive}
              sortOrder={sortOrder}
              onToggleInactive={setShowInactive}
              onSortChange={setSortOrder}
            >
              <EntityHeader
                title={sprintDraft.id ? sprintDraft.name || 'Sprint' : 'New sprint'}
                description="Sprints keep operational settings here and route back to a scoped task list."
                detailButton={sprintDraft.id ? { label: 'Details', icon: <FolderTree className="h-3.5 w-3.5" />, onClick: () => closeAndNavigate(`/sprints/${sprintDraft.id}`) } : undefined}
                opsButton={sprintDraft.id ? { label: 'Open in Operations', onClick: () => closeAndNavigate(`/operations?sprint_id=${sprintDraft.id}`) } : undefined}
              />
              <div className="grid gap-4 md:grid-cols-2">
                <Field label="Name *">
                  <Input value={sprintDraft.name} onChange={(e) => setSprintDraft((prev) => ({ ...prev, name: e.target.value }))} />
                </Field>
                <Field label="Status">
                  <select className="h-8 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-sm text-zinc-100" value={sprintDraft.status} onChange={(e) => setSprintDraft((prev) => ({ ...prev, status: e.target.value as 'active' | 'inactive' }))}>
                    <option value="active">Active</option>
                    <option value="inactive">Inactive</option>
                  </select>
                </Field>
                <Field label="Project">
                  <select className="h-8 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-sm text-zinc-100" value={sprintDraft.projectId} onChange={(e) => setSprintDraft((prev) => ({ ...prev, projectId: e.target.value }))}>
                    <option value={NONE}>No project</option>
                    {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
                  </select>
                </Field>
                <Field label="Approval mode">
                  <Input value={sprintDraft.approvalMode} onChange={(e) => setSprintDraft((prev) => ({ ...prev, approvalMode: e.target.value }))} />
                </Field>
                <Field label="Cost budget">
                  <Input value={sprintDraft.costBudget} onChange={(e) => setSprintDraft((prev) => ({ ...prev, costBudget: e.target.value }))} placeholder="Optional USD amount" />
                </Field>
                <Field label="Goal / description" className="md:col-span-2">
                  <Textarea value={sprintDraft.goal} onChange={(e) => setSprintDraft((prev) => ({ ...prev, goal: e.target.value }))} rows={4} />
                </Field>
              </div>
              <EntityActions
                onSave={saveSprint}
                onDelete={sprintDraft.id ? deleteSprint : undefined}
                saveDisabled={saving || !sprintDraft.name.trim()}
                saveLabel={sprintDraft.id ? 'Save sprint' : 'Create sprint'}
              />
            </EntityLayout>
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  )
}

function Field({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
  return (
    <div className={className}>
      <Label className="mb-1.5 block text-xs text-zinc-400">{label}</Label>
      {children}
    </div>
  )
}

function EntityLayout({
  title,
  items,
  selectedId,
  onSelect,
  onCreate,
  loading,
  showInactive,
  sortOrder,
  onToggleInactive,
  onSortChange,
  children,
}: {
  title: string
  items: Array<{ id: string; label: string; sublabel: string }>
  selectedId: string | null | undefined
  onSelect: (id: string | null) => void
  onCreate: () => void
  loading: boolean
  showInactive: boolean
  sortOrder: 'title' | 'updated'
  onToggleInactive: (next: boolean) => void
  onSortChange: (next: 'title' | 'updated') => void
  children: ReactNode
}) {
  return (
    <div className="grid h-full min-h-0 grid-cols-[280px,minmax(0,1fr)] gap-4 overflow-hidden">
      <div className="flex min-h-0 min-w-0 flex-col rounded-xl border border-zinc-800 bg-zinc-950/60">
        <ListToolbar
          title={title}
          showInactive={showInactive}
          sortOrder={sortOrder}
          onToggleInactive={onToggleInactive}
          onSortChange={onSortChange}
          onCreate={onCreate}
        />
        <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto p-2" tabIndex={0}>
          {items.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => onSelect(item.id)}
              className={`mb-2 flex w-full flex-col rounded-lg border px-3 py-2 text-left transition-colors ${
                selectedId === item.id ? 'border-zinc-500 bg-zinc-900 text-zinc-100' : 'border-zinc-800 bg-zinc-950/50 text-zinc-300 hover:bg-zinc-900/70'
              }`}
            >
              <span className="text-sm font-medium">{item.label}</span>
              <span className="text-[11px] text-zinc-500">{item.sublabel}</span>
            </button>
          ))}
          {!loading && items.length === 0 && <div className="px-2 py-4 text-sm text-zinc-500">Nothing here yet.</div>}
        </div>
      </div>
      <div className="no-scrollbar min-h-0 min-w-0 overflow-y-auto rounded-xl border border-zinc-800 bg-zinc-950/60 p-4" tabIndex={0}>
        {children}
      </div>
    </div>
  )
}

function ListToolbar({
  title,
  showInactive,
  sortOrder,
  onToggleInactive,
  onSortChange,
  onCreate,
}: {
  title: string
  showInactive: boolean
  sortOrder: 'title' | 'updated'
  onToggleInactive: (next: boolean) => void
  onSortChange: (next: 'title' | 'updated') => void
  onCreate: () => void
}) {
  return (
    <div className="border-b border-zinc-800 px-3 py-2">
      <div className="mb-2 flex items-center justify-between">
        <div className="text-xs uppercase tracking-[0.18em] text-zinc-500">{title}</div>
        <Button type="button" size="icon-xs" variant="outline" aria-label={`Create ${title.slice(0, -1).toLowerCase()}`} onClick={onCreate}>
          <Plus className="h-3.5 w-3.5" />
        </Button>
      </div>
      <div className="flex items-center gap-2">
        <label className="flex items-center gap-1.5 text-[11px] text-zinc-400">
          <input type="checkbox" checked={showInactive} onChange={(e) => onToggleInactive(e.target.checked)} />
          Show inactive
        </label>
        <select
          className="h-7 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-[11px] text-zinc-200"
          value={sortOrder}
          onChange={(e) => onSortChange(e.target.value as 'title' | 'updated')}
        >
          <option value="updated">Last updated</option>
          <option value="title">Title</option>
        </select>
      </div>
    </div>
  )
}

function sortItems<T extends { name: string; status: string; updated_at: string }>(
  items: T[],
  showInactive: boolean,
  sortOrder: 'title' | 'updated'
): T[] {
  const filtered = showInactive ? items : items.filter((item) => item.status === 'active')
  return [...filtered].sort((a, b) => {
    if (sortOrder === 'title') {
      return a.name.localeCompare(b.name)
    }
    return b.updated_at.localeCompare(a.updated_at)
  })
}

function EntityHeader({
  title,
  description,
  detailButton,
  opsButton,
}: {
  title: string
  description: string
  detailButton?: { label: string; icon: ReactNode; onClick: () => void }
  opsButton?: { label: string; onClick: () => void }
}) {
  return (
    <div className="mb-4 flex items-center justify-between gap-3">
      <div>
        <h3 className="text-base font-semibold text-zinc-100">{title}</h3>
        <p className="text-sm text-zinc-500">{description}</p>
      </div>
      <div className="flex items-center gap-2">
        {detailButton && (
          <Button variant="outline" size="sm" onClick={detailButton.onClick}>
            {detailButton.icon}
            {detailButton.label}
          </Button>
        )}
        {opsButton && (
          <Button variant="outline" size="sm" onClick={opsButton.onClick}>
            <ExternalLink className="h-3.5 w-3.5" />
            {opsButton.label}
          </Button>
        )}
      </div>
    </div>
  )
}

function EntityActions({
  onSave,
  onDelete,
  saveDisabled,
  saveLabel,
}: {
  onSave: () => void
  onDelete?: () => void
  saveDisabled: boolean
  saveLabel: string
}) {
  return (
    <div className="mt-4 flex items-center gap-2">
      <Button onClick={onSave} disabled={saveDisabled}>{saveLabel}</Button>
      {onDelete && (
        <Button variant="destructive" onClick={onDelete}>
          <Trash2 className="h-3.5 w-3.5" />
          Delete
        </Button>
      )}
    </div>
  )
}
