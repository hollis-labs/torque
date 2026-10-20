import { useNavigate, useParams } from 'react-router-dom'
import { PageHeader, Tabs, TabsList, TabsTrigger } from '@hollis-labs/sysop-ui'
import { FleetAgentsPanel } from '@/components/agents/fleet-agents'
import { AgentDirectoryView } from '@/console/agents'

export default function AgentsPage() {
  const navigate = useNavigate()
  const { tab } = useParams<{ tab?: string }>()
  const directory = tab === 'directory'
  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="Agents" />
      <Tabs value={directory ? 'directory' : 'fleet'} onValueChange={value => navigate(value === 'fleet' ? '/agents' : '/agents/directory')}>
        <TabsList>
          <TabsTrigger value="fleet">Fleet</TabsTrigger>
          <TabsTrigger value="directory">Directory</TabsTrigger>
        </TabsList>
      </Tabs>
      <div className="min-h-0 flex-1 overflow-auto">
        {directory ? <AgentDirectoryView /> : <FleetAgentsPanel />}
      </div>
    </div>
  )
}
