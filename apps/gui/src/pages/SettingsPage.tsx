import { useState, useEffect } from 'react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { useApi } from '@/hooks/use-api'
import type { FeatureFlags } from '@/lib/types'

export default function SettingsPage() {
  const api = useApi()
  const [flags, setFlags] = useState<FeatureFlags | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState<keyof FeatureFlags | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api.getFeatureFlags().then(setFlags).catch((err: Error) => {
      setError(err.message)
    }).finally(() => {
      setLoading(false)
    })
  }, [api])

  async function handleToggle(key: keyof FeatureFlags, value: boolean) {
    if (!flags) return
    setSaving(key)
    const updated = { ...flags, [key]: value }
    setFlags(updated) // optimistic
    try {
      await api.setSetting(`feature-flags.${key}`, String(value))
    } catch {
      setFlags(flags) // revert on error
    } finally {
      setSaving(null)
    }
  }

  return (
    <div className="flex h-full flex-col overflow-auto">
      <div className="border-b border-border bg-card px-6 py-4">
        <h1 className="text-lg font-semibold text-foreground">Settings</h1>
      </div>

      <div className="p-6 max-w-2xl">
        <Tabs defaultValue="features">
          <TabsList className="mb-6">
            <TabsTrigger value="features">Feature Flags</TabsTrigger>
            <TabsTrigger value="about">About</TabsTrigger>
          </TabsList>

          <TabsContent value="features">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">Feature Flags</CardTitle>
                <CardDescription>
                  Enable or disable optional features in Clockwork Manifold.
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-5">
                {loading ? (
                  Array.from({ length: 3 }).map((_, i) => (
                    <div key={i} className="flex items-center justify-between gap-4">
                      <div className="flex flex-col gap-1.5">
                        <Skeleton className="h-4 w-24" />
                        <Skeleton className="h-3 w-48" />
                      </div>
                      <Skeleton className="h-6 w-11 rounded-full" />
                    </div>
                  ))
                ) : error ? (
                  <div className="py-4 text-center">
                    <p className="text-sm text-muted-foreground">{error}</p>
                    <button className="mt-2 text-xs text-primary hover:underline" onClick={() => window.location.reload()}>Retry</button>
                  </div>
                ) : flags ? (
                  <>
                    <FlagRow
                      label="Sprints"
                      description="Organize tasks into time-boxed sprints."
                      checked={flags.sprints}
                      disabled={saving === 'sprints'}
                      onCheckedChange={(v) => handleToggle('sprints', v)}
                    />
                    <FlagRow
                      label="Projects"
                      description="Group tasks under named projects."
                      checked={flags.projects}
                      disabled={saving === 'projects'}
                      onCheckedChange={(v) => handleToggle('projects', v)}
                    />
                    <FlagRow
                      label="Epics"
                      description="Attach tasks to high-level epics for roadmap tracking."
                      checked={flags.epics}
                      disabled={saving === 'epics'}
                      onCheckedChange={(v) => handleToggle('epics', v)}
                    />
                  </>
                ) : null}
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="about">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">Clockwork Manifold</CardTitle>
                <CardDescription>Task orchestration engine for autonomous agents.</CardDescription>
              </CardHeader>
              <CardContent>
                <dl className="flex flex-col gap-3 text-sm">
                  <div className="flex justify-between">
                    <dt className="text-muted-foreground">Version</dt>
                    <dd className="font-mono font-medium">0.1.0</dd>
                  </div>
                  <div className="flex justify-between">
                    <dt className="text-muted-foreground">API</dt>
                    <dd className="font-mono font-medium">/api/v1</dd>
                  </div>
                </dl>
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}

interface FlagRowProps {
  label: string
  description: string
  checked: boolean
  disabled?: boolean
  onCheckedChange: (value: boolean) => void
}

function FlagRow({ label, description, checked, disabled, onCheckedChange }: FlagRowProps) {
  const id = `flag-${label.toLowerCase()}`
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="flex flex-col gap-0.5">
        <Label htmlFor={id} className="text-sm font-medium cursor-pointer">{label}</Label>
        <p className="text-xs text-muted-foreground">{description}</p>
      </div>
      <Switch
        id={id}
        checked={checked}
        onCheckedChange={onCheckedChange}
        disabled={disabled}
      />
    </div>
  )
}
