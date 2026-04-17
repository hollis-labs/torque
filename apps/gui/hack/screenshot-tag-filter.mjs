// Screenshot script for CW-20260417-0090 (tag filter dropdown).
// Mocks /tasks, /tags, /projects, /sprints, /epics, and feature-flags so the
// board renders deterministically. Captures: overview unfiltered, dropdown
// open, and filtered result (URL has ?tag=needs-discussion).

import { chromium } from 'playwright-core'
import { spawn } from 'node:child_process'
import path from 'node:path'
import fs from 'node:fs'

const ROOT = path.resolve(new URL('.', import.meta.url).pathname, '..')
const ARTIFACTS = path.resolve(ROOT, 'artifacts', 'CW-20260417-0090')
fs.mkdirSync(ARTIFACTS, { recursive: true })

const PORT = 4320
const BASE = `http://localhost:${PORT}`

const TAGS = [
  { slug: 'new', name: 'new', description: '', color: 'blue', created_at: '', updated_at: '' },
  { slug: 'needs-discussion', name: 'needs-discussion', description: '', color: 'amber', created_at: '', updated_at: '' },
  { slug: 'bug', name: 'bug', description: '', color: 'red', created_at: '', updated_at: '' },
  { slug: 'ui', name: 'ui', description: '', color: 'violet', created_at: '', updated_at: '' },
]

function mkTask(id, title, status, priority, tags) {
  return {
    id, title, description: '', status, priority,
    kind: 'agent', executor: 'cli', manual: false,
    on_done: 'review', on_fail: 'retry', on_review: 'pause',
    max_retries: 3, source_type: 'user', trust: 'normal',
    checkpoint_mode: 'none', on_checkpoint_response: 'resume',
    cost_budget: null, token_budget: null, max_duration_ms: null,
    depends_on: [], working_dir: '', system_prompt: '', agent_profile: '',
    tools: [], permissions: {}, environment: {}, files: [],
    deliverables: [], deliverable_preset: '', escalation_chain: [],
    quality_gates: [], blocked_reason: '', metadata: {}, source_ref: '',
    sprint_id: null, project_id: null, epic_id: null, on_done_merge: 'none',
    created_at: '2026-04-17T05:00:00Z', updated_at: '2026-04-17T05:30:00Z',
    tags: tags.map((slug) => TAGS.find((t) => t.slug === slug)),
  }
}

const ALL = [
  mkTask('CW-0001', 'Wire tag filter dropdown', 'doing', 2, ['new', 'ui']),
  mkTask('CW-0002', 'Refactor API retry logic', 'todo', 1, ['needs-discussion']),
  mkTask('CW-0003', 'Fix race in SSE reconnect', 'review', 1, ['bug']),
  mkTask('CW-0004', 'Doc the executor interface', 'todo', 3, []),
  mkTask('CW-0005', 'Spike: should filter multi-select?', 'backlog', 2, ['needs-discussion', 'ui']),
]

function startPreview() {
  const proc = spawn('./node_modules/.bin/vite', ['preview', '--port', String(PORT), '--strictPort'], {
    cwd: ROOT,
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('vite preview timeout')), 15_000)
    proc.stdout.on('data', (buf) => {
      if (buf.toString().includes('Local:')) { clearTimeout(timer); resolve(proc) }
    })
    proc.stderr.on('data', (buf) => process.stderr.write(buf))
    proc.on('exit', (code) => { if (code !== 0) reject(new Error(`vite exited ${code}`)) })
  })
}

async function run() {
  const preview = await startPreview()
  const browser = await chromium.launch({ headless: true })
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 }, colorScheme: 'dark' })

  await ctx.route('**/api/v1/feature-flags', (r) => r.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ sprints: true, projects: true, epics: true }),
  }))
  await ctx.route('**/api/v1/tags', (r) => r.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ tags: TAGS }),
  }))
  await ctx.route('**/api/v1/projects*', (r) => r.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ projects: [] }),
  }))
  await ctx.route('**/api/v1/sprints*', (r) => r.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ sprints: [] }),
  }))
  await ctx.route('**/api/v1/epics*', (r) => r.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ epics: [] }),
  }))
  await ctx.route('**/api/v1/events', (r) => r.fulfill({
    status: 200,
    headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', 'Connection': 'keep-alive' },
    body: '',
  }))

  // Filter tasks server-side when ?tags= present so reload shows filtered state.
  await ctx.route('**/api/v1/tasks*', (r) => {
    const url = new URL(r.request().url())
    const tagParam = url.searchParams.get('tags')
    let out = ALL
    if (tagParam) {
      const slugs = tagParam.split(',')
      out = ALL.filter((t) => t.tags.some((tg) => slugs.includes(tg.slug)))
    }
    r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ tasks: out }) })
  })

  const page = await ctx.newPage()
  page.on('pageerror', (err) => console.error('pageerror:', err.message))

  try {
    // 1. Overview, unfiltered.
    await page.goto(BASE, { waitUntil: 'networkidle' })
    await page.waitForTimeout(400)
    await page.screenshot({ path: path.join(ARTIFACTS, '01-overview.png'), fullPage: true })

    // 2. Tag dropdown opened.
    const tagTrigger = page.getByRole('combobox', { name: /filter by tag/i })
    await tagTrigger.click()
    await page.waitForTimeout(200)
    await page.screenshot({ path: path.join(ARTIFACTS, '02-tag-dropdown-open.png'), fullPage: true })

    // 3. Filtered by needs-discussion (URL param persisted).
    await page.goto(`${BASE}/?tag=needs-discussion`, { waitUntil: 'networkidle' })
    await page.waitForTimeout(400)
    await page.screenshot({ path: path.join(ARTIFACTS, '03-filtered-needs-discussion.png'), fullPage: true })

    console.log('Saved screenshots to', path.relative(process.cwd(), ARTIFACTS))
  } finally {
    await browser.close()
    preview.kill('SIGTERM')
  }
}

run().catch((err) => { console.error(err); process.exitCode = 1 })
