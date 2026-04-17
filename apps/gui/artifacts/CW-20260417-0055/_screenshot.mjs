import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

const OUT = resolve('apps/gui/artifacts/CW-20260417-0055')
mkdirSync(OUT, { recursive: true })

const BASE = 'http://localhost:5175'
const EMPTY_TASK = 'CW-20260408-0002'
const POPULATED_TASK = 'CW-20260408-0001'

// Seed one artifact on the populated task so re-runs against a clean
// dogfood DB still produce the populated screenshot. Cleaned up at the
// end so the empty/populated state isn't baked into the backend.
const API = `${BASE}/api/v1`
const seedRes = await fetch(`${API}/artifacts`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    task_id: POPULATED_TASK,
    type: 'log',
    content: 'CW-20260417-0055 smoke artifact',
  }),
})
if (!seedRes.ok) {
  throw new Error(`seed artifact failed: ${seedRes.status}`)
}
const seeded = await seedRes.json()

const browser = await chromium.launch()
const ctx = await browser.newContext({
  viewport: { width: 1280, height: 1000 },
  colorScheme: 'dark',
})
const page = await ctx.newPage()

page.on('console', (m) => console.log('[page]', m.type(), m.text()))
page.on('pageerror', (e) => console.log('[err]', e.message))

// 1) Empty-artifacts case — no crash, empty state renders
await page.goto(`${BASE}/tasks/${EMPTY_TASK}`, { waitUntil: 'domcontentloaded' })
await page.getByRole('tab', { name: /Artifacts/i }).waitFor({ timeout: 15_000 })
await page.getByRole('tab', { name: /Artifacts/i }).click()
// Empty-state heading from EmptyState component
await page.getByText('No artifacts have been produced for this task.').waitFor({ timeout: 10_000 })
await page.waitForTimeout(200)
await page.screenshot({
  path: `${OUT}/01-artifacts-empty.png`,
  fullPage: false,
})

// 2) Populated-artifacts case — list renders
await page.goto(`${BASE}/tasks/${POPULATED_TASK}`, { waitUntil: 'domcontentloaded' })
await page.getByRole('tab', { name: /Artifacts/i }).waitFor({ timeout: 15_000 })
await page.getByRole('tab', { name: /Artifacts/i }).click()
await page.waitForSelector('button[aria-label^="Delete artifact"]', { timeout: 15_000 })
await page.waitForTimeout(200)
await page.screenshot({
  path: `${OUT}/02-artifacts-populated.png`,
  fullPage: false,
})

await browser.close()

// Best-effort cleanup: the dogfood binary behind the dev proxy may
// predate the DELETE /artifacts/{id} route and silently fall through
// to the SPA handler, in which case the seed will persist. Re-running
// is still safe — we just create a new row each time.
await fetch(`${API}/artifacts/${seeded.id}`, { method: 'DELETE' }).catch(() => {})

console.log('screenshots written to', OUT)
