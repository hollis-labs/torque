// One-shot screenshot script for CW-20260417-0053 (run.progress heartbeat
// + tool_use kinds). Mocks the backend and SSE so the Activity panel renders
// tool_use rows (Edit/Bash/Read/Grep) plus the "Agent working…" empty-state
// flip driven by heartbeats. Saves two PNGs under
// apps/gui/artifacts/CW-20260417-0053/.

import { chromium } from 'playwright-core'
import { spawn } from 'node:child_process'
import path from 'node:path'
import fs from 'node:fs'

const ROOT = path.resolve(new URL('.', import.meta.url).pathname, '..')
const ARTIFACTS = path.resolve(ROOT, 'artifacts', 'CW-20260417-0053')

fs.mkdirSync(ARTIFACTS, { recursive: true })

const PREVIEW_PORT = 4319
const BASE_URL = `http://localhost:${PREVIEW_PORT}`

const taskBase = {
  status: 'doing',
  priority: 1,
  kind: 'agent',
  executor: 'cli',
  manual: false,
  on_done: 'review',
  on_fail: 'retry',
  on_review: 'pause',
  max_retries: 3,
  source_type: 'user',
  trust: 'normal',
  checkpoint_mode: 'none',
  on_checkpoint_response: 'resume',
  cost_budget: null,
  token_budget: null,
  max_duration_ms: null,
  depends_on: [],
  working_dir: '',
  system_prompt: '',
  agent_profile: '',
  tools: [],
  permissions: {},
  environment: {},
  files: [],
  deliverables: [],
  deliverable_preset: '',
  escalation_chain: [],
  quality_gates: [],
  blocked_reason: '',
  metadata: {},
  source_ref: '',
  sprint_id: null,
  project_id: null,
  epic_id: null,
  on_done_merge: 'none',
  tags: [],
}

const tasks = [
  {
    ...taskBase,
    id: 'CW-DEMO-0001',
    title: 'Active run — tool_use feed',
    description: 'Agent iterates on Edit / Bash / Read calls.',
    created_at: '2026-04-17T05:00:00Z',
    updated_at: '2026-04-17T05:40:00Z',
  },
  {
    ...taskBase,
    id: 'CW-DEMO-0002',
    title: 'Active run — waiting state',
    description: 'Heartbeat-only run to exercise the empty-state copy.',
    created_at: '2026-04-17T05:10:00Z',
    updated_at: '2026-04-17T05:41:30Z',
  },
]

const DEMO_TOOL_TASK = tasks[0]
const DEMO_HEARTBEAT_TASK = tasks[1]

function startPreview() {
  const args = ['preview', '--port', String(PREVIEW_PORT), '--strictPort']
  const proc = spawn('./node_modules/.bin/vite', args, {
    cwd: ROOT,
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  return new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error('vite preview did not become ready')),
      15_000,
    )
    proc.stdout.on('data', (buf) => {
      const s = buf.toString()
      if (s.includes('Local:')) {
        clearTimeout(timer)
        resolve(proc)
      }
    })
    proc.stderr.on('data', (buf) => process.stderr.write(buf))
    proc.on('exit', (code) => {
      if (code !== 0) reject(new Error(`vite preview exited ${code}`))
    })
  })
}

function sseLines(events) {
  return events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join('')
}

async function run() {
  const preview = await startPreview()

  const browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({
    viewport: { width: 1280, height: 900 },
    colorScheme: 'dark',
  })

  await context.route('**/api/v1/feature-flags', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ sprints: true, projects: true, epics: true }),
    })
  })
  await context.route('**/api/v1/tasks?*', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ tasks }),
    })
  })
  await context.route('**/api/v1/tasks', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ tasks }),
    })
  })
  for (const t of tasks) {
    await context.route(`**/api/v1/tasks/${t.id}`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(t),
      })
    })
    await context.route(
      `**/api/v1/tasks/${t.id}/comments`,
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify([]),
        })
      },
    )
    await context.route(
      `**/api/v1/tasks/${t.id}/artifacts`,
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify([]),
        })
      },
    )
  }
  await context.route('**/api/v1/runs*', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ runs: [] }),
    })
  })
  await context.route('**/api/v1/projects*', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ projects: [] }),
    })
  })
  await context.route('**/api/v1/sprints*', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ sprints: [] }),
    })
  })
  await context.route('**/api/v1/epics*', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ epics: [] }),
    })
  })

  const startedIso = new Date(Date.now() - 120_000).toISOString()

  const events = [
    {
      type: 'run.started',
      data: {
        task_id: DEMO_TOOL_TASK.id,
        run_id: 201,
        payload: { executor: 'cli', started_at: startedIso },
      },
      timestamp: startedIso,
    },
    {
      type: 'run.progress',
      data: {
        task_id: DEMO_TOOL_TASK.id,
        run_id: 201,
        payload: {
          kind: 'heartbeat',
          elapsed_sec: 30,
          worker_id: 'worker-CW-DEMO-0001-201',
        },
      },
      timestamp: new Date(Date.now() - 90_000).toISOString(),
    },
    {
      type: 'run.progress',
      data: {
        task_id: DEMO_TOOL_TASK.id,
        run_id: 201,
        payload: {
          kind: 'tool_use',
          tool_name: 'Read',
          args_summary: '{"file_path":"apps/gui/src/hooks/use-active-runs.tsx"}',
        },
      },
      timestamp: new Date(Date.now() - 80_000).toISOString(),
    },
    {
      type: 'run.progress',
      data: {
        task_id: DEMO_TOOL_TASK.id,
        run_id: 201,
        payload: {
          kind: 'tool_use',
          tool_name: 'Grep',
          args_summary: '{"pattern":"run.progress","path":"internal"}',
        },
      },
      timestamp: new Date(Date.now() - 70_000).toISOString(),
    },
    {
      type: 'run.progress',
      data: {
        task_id: DEMO_TOOL_TASK.id,
        run_id: 201,
        payload: {
          kind: 'tool_use',
          tool_name: 'Edit',
          args_summary:
            '{"file_path":"apps/gui/src/components/domain/activity-panel.tsx","old_string":"Waiting…","new_string":"Agent working…"}',
        },
      },
      timestamp: new Date(Date.now() - 50_000).toISOString(),
    },
    {
      type: 'run.progress',
      data: {
        task_id: DEMO_TOOL_TASK.id,
        run_id: 201,
        payload: {
          kind: 'tool_use',
          tool_name: 'Bash',
          args_summary: '{"command":"go test ./internal/runtime/scheduler/..."}',
        },
      },
      timestamp: new Date(Date.now() - 20_000).toISOString(),
    },
    {
      type: 'run.progress',
      data: {
        task_id: DEMO_TOOL_TASK.id,
        run_id: 201,
        payload: {
          kind: 'note',
          text: 'All tests pass. Moving to FE integration.',
        },
      },
      timestamp: new Date(Date.now() - 10_000).toISOString(),
    },
    // Second run: heartbeat-only to demo "Agent working…" empty-state flip.
    {
      type: 'run.started',
      data: {
        task_id: DEMO_HEARTBEAT_TASK.id,
        run_id: 202,
        payload: {
          executor: 'cli',
          started_at: new Date(Date.now() - 35_000).toISOString(),
        },
      },
      timestamp: new Date(Date.now() - 35_000).toISOString(),
    },
    {
      type: 'run.progress',
      data: {
        task_id: DEMO_HEARTBEAT_TASK.id,
        run_id: 202,
        payload: {
          kind: 'heartbeat',
          elapsed_sec: 30,
          worker_id: 'worker-CW-DEMO-0002-202',
        },
      },
      timestamp: new Date(Date.now() - 5_000).toISOString(),
    },
  ]

  await context.route('**/api/v1/events', async (route) => {
    await route.fulfill({
      status: 200,
      headers: {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
      },
      body: sseLines(events),
    })
  })

  const page = await context.newPage()
  page.on('pageerror', (err) => console.error('pageerror:', err.message))
  page.on('console', (msg) => {
    if (msg.type() === 'error') console.error('console:', msg.text())
  })

  try {
    // Task 1 detail — Activity panel shows tool_use rows mixed with a note.
    await page.goto(`${BASE_URL}/tasks/${DEMO_TOOL_TASK.id}`, {
      waitUntil: 'networkidle',
    })
    await page.waitForTimeout(800)
    await page.screenshot({
      path: path.join(ARTIFACTS, '01-tool-use-feed.png'),
      fullPage: true,
    })

    // Task 2 detail — heartbeat-only run shows "Agent working…" copy
    // instead of the "Waiting for the agent's first update…" dead text.
    await page.goto(`${BASE_URL}/tasks/${DEMO_HEARTBEAT_TASK.id}`, {
      waitUntil: 'networkidle',
    })
    await page.waitForTimeout(800)
    await page.screenshot({
      path: path.join(ARTIFACTS, '02-heartbeat-agent-working.png'),
      fullPage: true,
    })

    console.log(
      `Saved screenshots to ${path.relative(process.cwd(), ARTIFACTS)}`,
    )
  } finally {
    await browser.close()
    preview.kill('SIGTERM')
  }
}

run().catch((err) => {
  console.error(err)
  process.exitCode = 1
})
