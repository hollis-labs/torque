// One-shot screenshot script for CW-20260417-0032 (run-level SSE UI).
// Launches the built SPA via `vite preview`, mocks the backend (`/api/v1/*`
// plus the SSE `/events` endpoint) using playwright page.route so we can
// deterministically show active-run pulse + Activity panel without a live
// clockwork server. Artifacts land under apps/gui/artifacts/CW-20260417-0032/.

import { chromium } from 'playwright-core'
import { spawn } from 'node:child_process'
import path from 'node:path'
import fs from 'node:fs'

const ROOT = path.resolve(new URL('.', import.meta.url).pathname, '..')
const ARTIFACTS = path.resolve(
  ROOT,
  'artifacts',
  'CW-20260417-0032',
)

fs.mkdirSync(ARTIFACTS, { recursive: true })

const PREVIEW_PORT = 4318
const BASE_URL = `http://localhost:${PREVIEW_PORT}`

const tasks = [
  {
    id: 'CW-DEMO-0001',
    title: 'Build the auth refresh endpoint',
    description: 'Add /auth/refresh with rotating tokens.',
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
    created_at: '2026-04-17T05:00:00Z',
    updated_at: '2026-04-17T05:40:00Z',
    tags: [],
  },
  {
    id: 'CW-DEMO-0002',
    title: 'Wire run.progress events into Activity panel',
    description: 'Render notes + artifacts as they stream.',
    status: 'doing',
    priority: 2,
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
    created_at: '2026-04-17T05:10:00Z',
    updated_at: '2026-04-17T05:41:30Z',
    tags: [],
  },
  {
    id: 'CW-DEMO-0003',
    title: 'Ship weekly release notes',
    description: 'Summarize changes since 2026-04-10.',
    status: 'review',
    priority: 3,
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
    created_at: '2026-04-16T11:00:00Z',
    updated_at: '2026-04-17T03:00:00Z',
    tags: [],
  },
]

const DEMO_TASK = tasks[0]

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
  return events
    .map((e) => `data: ${JSON.stringify(e)}\n\n`)
    .join('')
}

async function run() {
  const preview = await startPreview()

  const browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    colorScheme: 'dark',
  })

  // Mock the REST surface the SPA touches on boot/navigation.
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
  await context.route(`**/api/v1/tasks/${DEMO_TASK.id}`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(DEMO_TASK),
    })
  })
  await context.route(
    `**/api/v1/tasks/${DEMO_TASK.id}/comments`,
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    },
  )
  await context.route(
    `**/api/v1/tasks/${DEMO_TASK.id}/artifacts`,
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    },
  )
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

  // Mock the SSE stream with two active runs — one per demo task. Each run
  // gets a run.started and a run.progress (note) so the pulse tooltip and
  // Activity panel both have something to show.
  const startedIso = new Date(Date.now() - 90_000).toISOString() // 1m30s ago
  const events = [
    {
      type: 'run.started',
      data: {
        task_id: tasks[0].id,
        run_id: 101,
        payload: { executor: 'cli', started_at: startedIso },
      },
      timestamp: startedIso,
    },
    {
      type: 'run.progress',
      data: {
        task_id: tasks[0].id,
        run_id: 101,
        payload: { kind: 'note', text: 'Scaffolding handler and token store' },
      },
      timestamp: new Date(Date.now() - 60_000).toISOString(),
    },
    {
      type: 'run.progress',
      data: {
        task_id: tasks[0].id,
        run_id: 101,
        payload: {
          kind: 'artifact',
          artifact_type: 'diff',
          file_path: 'internal/auth/refresh.go',
          content: 'func RefreshToken(ctx context.Context) ...',
        },
      },
      timestamp: new Date(Date.now() - 30_000).toISOString(),
    },
    {
      type: 'run.progress',
      data: {
        task_id: tasks[0].id,
        run_id: 101,
        payload: { kind: 'tokens', prompt: 2400, completion: 860, cost: 0.042 },
      },
      timestamp: new Date(Date.now() - 10_000).toISOString(),
    },
    {
      type: 'run.started',
      data: {
        task_id: tasks[1].id,
        run_id: 102,
        payload: {
          executor: 'cli',
          started_at: new Date(Date.now() - 20_000).toISOString(),
        },
      },
      timestamp: new Date(Date.now() - 20_000).toISOString(),
    },
    {
      type: 'run.progress',
      data: {
        task_id: tasks[1].id,
        run_id: 102,
        payload: { kind: 'note', text: 'Rendering the Activity panel' },
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
        'Connection': 'keep-alive',
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
    // Board page — overview with pulsing doing rows.
    await page.goto(`${BASE_URL}/?status=doing,todo,review`, {
      waitUntil: 'networkidle',
    })
    // Give EventSource a tick to land the buffered events.
    await page.waitForTimeout(600)
    await page.screenshot({
      path: path.join(ARTIFACTS, '01-board-pulse.png'),
      fullPage: true,
    })

    // Task detail page — Activity panel.
    await page.goto(`${BASE_URL}/tasks/${DEMO_TASK.id}`, {
      waitUntil: 'networkidle',
    })
    await page.waitForTimeout(600)
    await page.screenshot({
      path: path.join(ARTIFACTS, '02-detail-activity.png'),
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
